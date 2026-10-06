package mihomo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

// Manager 管理 Mihomo 的安装、配置、子进程和控制 API。
type Manager struct {
	lifecycle sync.Mutex // 串行启停，保证旧进程完全回收后才能启动新进程。
	mu        sync.RWMutex
	store     *appconfig.Store
	installer *Installer
	logs      *core.LogBuffer
	state     core.State
	lastError string
	cmd       *exec.Cmd
	done      chan struct{}
	// providerListen 是控制服务本次实际监听地址，只影响运行时生成的内部 provider URL，
	// 不会把 serve --listen 的临时覆盖值写回用户配置。
	providerListen string
	options        RuntimeOptions
}

// SetProviderListen 设置 Mihomo 访问 Kivo 内部订阅适配器时使用的实际监听地址。
func (m *Manager) SetProviderListen(listen string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerListen = strings.TrimSpace(listen)
}

// NewManager 创建 Mihomo 管理器。
func NewManager(store *appconfig.Store) *Manager {
	paths := store.Paths()
	state := core.StateStopped
	if store.Snapshot().Mihomo.BinaryPath == "" {
		state = core.StateNotInstalled
	}
	return &Manager{
		store:     store,
		installer: NewInstaller(paths.CoreDir, paths.DownloadDir),
		logs:      core.NewLogBuffer(2000),
		state:     state,
	}
}

// Install 下载官方内核并更新活动版本。
func (m *Manager) Install(ctx context.Context, version string, progress func(core.InstallEvent)) error {
	cfg := m.store.Snapshot()
	if err := m.installer.ConfigureDownload(cfg.Mihomo.DownloadRetry, cfg.Mihomo.DownloadProxy); err != nil {
		return err
	}
	wasRunning := m.Status(ctx).State == core.StateRunning
	paused := false
	path, installedVersion, err := m.installer.install(ctx, version, progress, func(target string) error {
		if wasRunning && filepath.Clean(target) == filepath.Clean(cfg.Mihomo.BinaryPath) {
			if err := m.Stop(ctx); err != nil {
				return err
			}
			paused = true
		}
		return nil
	})
	if err != nil {
		if paused {
			_ = m.Start(ctx)
		}
		m.logs.Add("安装失败: " + sanitizeLog(err.Error()))
		return err
	}
	if wasRunning && !paused {
		if err := m.Stop(ctx); err != nil {
			return err
		}
	}
	if err := m.activateInstallation(path, installedVersion); err != nil {
		return err
	}
	if wasRunning {
		if err := m.Start(ctx); err != nil {
			_ = m.store.Update(func(next *appconfig.Config) error { next.Mihomo = cfg.Mihomo; return nil })
			_ = m.Start(ctx)
			return fmt.Errorf("新安装版本启动失败，已回滚到 %s: %w", cfg.Mihomo.Version, err)
		}
	}
	m.mu.Lock()
	if m.state == core.StateNotInstalled || m.state == core.StateFailed {
		m.state = core.StateStopped
		m.lastError = ""
	}
	m.mu.Unlock()
	m.logs.Add("Mihomo " + installedVersion + " 已安装")
	if progress != nil {
		progress(core.InstallEvent{Stage: "done", Message: "Mihomo " + installedVersion + " 安装完成"})
	}
	return nil
}

// ListInstallations 扫描受管目录，返回可切换的全部 Mihomo 版本。
func (m *Manager) ListInstallations() ([]core.Installation, error) {
	root := filepath.Join(m.store.Paths().CoreDir, "mihomo")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []core.Installation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取内核目录: %w", err)
	}
	active := filepath.Clean(m.store.Snapshot().Mihomo.BinaryPath)
	items := make([]core.Installation, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		binary := filepath.Join(root, entry.Name(), executableName())
		if info, statErr := os.Stat(binary); statErr != nil || info.IsDir() {
			continue
		}
		version := entry.Name()
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		items = append(items, core.Installation{Engine: "mihomo", Version: version, Path: binary, Active: filepath.Clean(binary) == active})
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Version > items[right].Version })
	return items, nil
}

// Import 导入用户已经下载的官方 .gz/.zip 文件，并将其设为活动版本。
func (m *Manager) Import(ctx context.Context, source string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		return fmt.Errorf("安装包不可用: %s", source)
	}
	name := filepath.Base(source)
	match := regexp.MustCompile(`(?i)-v([0-9]+\.[0-9]+\.[0-9]+).*\.(?:gz|zip)$`).FindStringSubmatch(name)
	if len(match) != 2 {
		return errors.New("无法从文件名识别版本，请保留官方文件名，例如 mihomo-darwin-arm64-v1.19.31.gz")
	}
	version := "v" + match[1]
	dir := filepath.Join(m.store.Paths().CoreDir, "mihomo", match[1])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	target := filepath.Join(dir, executableName())
	// 先在旁路文件中解压和执行 -v 验证，避免损坏已有可用版本。
	staged, err := os.CreateTemp(dir, ".import-*")
	if err != nil {
		return err
	}
	stage := staged.Name()
	staged.Close()
	os.Remove(stage)
	defer os.Remove(stage)
	if err := extractBinary(source, name, stage); err != nil {
		return fmt.Errorf("导入 Mihomo: %w", err)
	}
	verify := exec.CommandContext(ctx, stage, "-v")
	platform.ConfigureCoreProcess(verify)
	if output, err := verify.CombinedOutput(); err != nil {
		return fmt.Errorf("验证导入内核失败: %s", sanitizeLog(string(output)))
	}
	sameActive := filepath.Clean(target) == filepath.Clean(m.store.Snapshot().Mihomo.BinaryPath)
	wasRunning := sameActive && m.Status(ctx).State == core.StateRunning
	if wasRunning {
		if err := m.Stop(ctx); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if wasRunning {
			_ = m.Start(ctx)
		}
		return fmt.Errorf("替换导入内核: %w", err)
	}
	if wasRunning {
		return m.Start(ctx)
	}
	// 统一走 Use 的停止、切换、启动和失败回滚流程，避免运行中直接替换配置。
	return m.Use(ctx, version)
}

// Use 原子切换活动版本；若 Core 正在运行，则启动失败时自动回滚。
func (m *Manager) Use(ctx context.Context, version string) error {
	items, err := m.ListInstallations()
	if err != nil {
		return err
	}
	version = normalizeVersion(version)
	var selected *core.Installation
	for index := range items {
		if normalizeVersion(items[index].Version) == version {
			selected = &items[index]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("未安装 Mihomo %s", version)
	}
	if selected.Active {
		return nil
	}
	old := m.store.Snapshot().Mihomo
	wasRunning := m.Status(ctx).State == core.StateRunning
	if wasRunning {
		if err := m.Stop(ctx); err != nil {
			return err
		}
	}
	if err := m.activateInstallation(selected.Path, selected.Version); err != nil {
		return err
	}
	if wasRunning {
		if err := m.Start(ctx); err != nil {
			_ = m.store.Update(func(cfg *appconfig.Config) error { cfg.Mihomo = old; return nil })
			_ = m.Start(ctx)
			return fmt.Errorf("新版本启动失败，已回滚到 %s: %w", old.Version, err)
		}
	}
	return nil
}

// Remove 删除一个非活动版本。
func (m *Manager) Remove(_ context.Context, version string) error {
	items, err := m.ListInstallations()
	if err != nil {
		return err
	}
	version = normalizeVersion(version)
	for _, item := range items {
		if normalizeVersion(item.Version) != version {
			continue
		}
		if item.Active {
			return errors.New("不能删除当前活动内核，请先使用 /core use 切换版本")
		}
		root := filepath.Clean(filepath.Join(m.store.Paths().CoreDir, "mihomo"))
		dir := filepath.Clean(filepath.Dir(item.Path))
		if filepath.Dir(dir) != root {
			return errors.New("拒绝删除受管内核目录之外的文件")
		}
		return os.RemoveAll(dir)
	}
	return fmt.Errorf("未安装 Mihomo %s", version)
}

// Purge 停止 Core 并删除所有受管 Mihomo 版本。
func (m *Manager) Purge(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	root := filepath.Clean(filepath.Join(m.store.Paths().CoreDir, "mihomo"))
	if filepath.Dir(root) != filepath.Clean(m.store.Paths().CoreDir) {
		return errors.New("内核目录校验失败")
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if err := m.store.Update(func(cfg *appconfig.Config) error {
		cfg.Mihomo.BinaryPath = ""
		cfg.Mihomo.Version = ""
		cfg.Mihomo.AutoStart = false
		return nil
	}); err != nil {
		return err
	}
	m.mu.Lock()
	m.state = core.StateNotInstalled
	m.lastError = ""
	m.mu.Unlock()
	return nil
}

func (m *Manager) activateInstallation(path, version string) error {
	return m.store.Update(func(cfg *appconfig.Config) error {
		cfg.Mihomo.BinaryPath = path
		cfg.Mihomo.Version = version
		return nil
	})
}

func normalizeVersion(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "v")
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "mihomo.exe"
	}
	return "mihomo"
}

// Start 先校验生成配置，再启动 Mihomo 并等待控制接口就绪。
func (m *Manager) Start(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	return m.start(ctx)
}

func (m *Manager) start(ctx context.Context) error {
	m.mu.Lock()
	if m.state == core.StateRunning || m.state == core.StateStarting {
		m.mu.Unlock()
		return nil
	}
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return errors.New("上一次 Mihomo 进程尚未完全退出，请先停止内核后重试")
	}
	cfg := m.store.Snapshot()
	if m.providerListen != "" {
		cfg.Web.Listen = m.providerListen
	}
	if cfg.Mihomo.BinaryPath == "" {
		m.state = core.StateNotInstalled
		m.mu.Unlock()
		return errors.New("Mihomo 尚未安装，请先执行 /core install mihomo")
	}
	if _, err := os.Stat(cfg.Mihomo.BinaryPath); err != nil {
		m.state = core.StateFailed
		m.lastError = "Mihomo 可执行文件不存在"
		m.mu.Unlock()
		return fmt.Errorf("检查 Mihomo 文件: %w", err)
	}
	m.state = core.StateStarting
	m.lastError = ""
	m.mu.Unlock()

	options, err := newRuntimeOptions()
	if err != nil {
		m.setFailure(err)
		return err
	}
	m.mu.Lock()
	m.options = options
	m.mu.Unlock()
	runtimeDir := filepath.Join(m.store.Paths().RuntimeDir, "mihomo")
	if _, err := GenerateConfig(cfg, runtimeDir, options); err != nil {
		m.setFailure(err)
		return err
	}
	validateCtx, cancelValidate := context.WithTimeout(ctx, 15*time.Second)
	defer cancelValidate()
	validation := exec.CommandContext(validateCtx, cfg.Mihomo.BinaryPath, "-t", "-d", runtimeDir)
	platform.ConfigureCoreProcess(validation)
	output, err := validation.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("Mihomo 配置校验失败: %s", sanitizeLog(string(output)))
		m.setFailure(err)
		return err
	}

	cmd := exec.Command(cfg.Mihomo.BinaryPath, "-d", runtimeDir)
	platform.ConfigureCoreProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.setFailure(err)
		return fmt.Errorf("连接 Mihomo 标准输出: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.setFailure(err)
		return fmt.Errorf("连接 Mihomo 错误输出: %w", err)
	}
	if err := cmd.Start(); err != nil {
		m.setFailure(err)
		return fmt.Errorf("启动 Mihomo: %w", err)
	}

	done := make(chan struct{})
	m.mu.Lock()
	m.cmd = cmd
	m.done = done
	m.mu.Unlock()
	go m.scanLogs(stdout)
	go m.scanLogs(stderr)
	go m.waitProcess(cmd, done)

	client := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey)
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return errors.New("Mihomo 在控制接口就绪前退出，请查看 /logs")
		case <-ctx.Done():
			_ = m.stopProcess(2 * time.Second)
			return ctx.Err()
		case <-deadline.C:
			_ = m.stopProcess(2 * time.Second)
			err := errors.New("Mihomo 已启动但控制接口在 12 秒内未就绪")
			m.setFailure(err)
			return err
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, time.Second)
			ready := client.Healthy(probeCtx)
			cancel()
			if ready {
				if err := waitMixedPort(ctx, cfg.Mihomo.MixedPort); err != nil {
					_ = m.stopProcess(2 * time.Second)
					m.setFailure(err)
					return err
				}
				if cfg.SelectedNode != "" {
					if err := client.SelectNode(ctx, cfg.SelectedNode); err != nil {
						// 订阅可能已经移除该节点：保留内核的可用回退选择并明确记录。
						m.logs.Add("无法恢复保存的节点，使用内核当前选择: " + sanitizeLog(err.Error()))
					}
				}
				if cfg.Mihomo.Mode == "global" {
					if err := client.SetMode(ctx, "global"); err != nil {
						_ = m.stopProcess(2 * time.Second)
						m.setFailure(err)
						return err
					}
				}
				m.mu.Lock()
				if m.cmd != cmd {
					m.mu.Unlock()
					return errors.New("Mihomo 启动期间已退出")
				}
				m.state = core.StateRunning
				m.lastError = ""
				m.mu.Unlock()
				m.logs.Add("Mihomo 控制接口已就绪")
				return nil
			}
		}
	}
}

// Stop 优雅停止进程，超时后强制终止。
func (m *Manager) Stop(_ context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	return m.stop()
}

func (m *Manager) stop() error {
	m.mu.Lock()
	if m.cmd == nil || m.cmd.Process == nil {
		if m.store.Snapshot().Mihomo.BinaryPath == "" {
			m.state = core.StateNotInstalled
		} else {
			m.state = core.StateStopped
		}
		m.mu.Unlock()
		return nil
	}
	m.state = core.StateStopping
	m.mu.Unlock()
	return m.stopProcess(5 * time.Second)
}

func (m *Manager) stopProcess(timeout time.Duration) error {
	m.mu.RLock()
	cmd, done := m.cmd, m.done
	m.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := platform.SignalCoreStop(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		m.logs.Add("Mihomo 未在期限内退出，执行强制终止")
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-done:
			return nil
		case <-time.After(3 * time.Second):
			return errors.New("Mihomo 进程尚未完全退出，拒绝启动重叠实例")
		}
	}
}

// Restart 使用最新持久化配置重新启动内核。
func (m *Manager) Restart(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	// 运行中重载先用当前活动内核验证新配置。预检失败时保留旧进程，
	// 避免端口、路由或订阅编辑错误导致代理无意中断。
	if m.Status(ctx).State == core.StateRunning {
		cfg := m.store.Snapshot()
		m.mu.RLock()
		if m.providerListen != "" {
			cfg.Web.Listen = m.providerListen
		}
		options := m.options
		m.mu.RUnlock()
		runtimeDir := filepath.Join(m.store.Paths().RuntimeDir, "mihomo")
		if _, err := GenerateConfig(cfg, runtimeDir, options); err != nil {
			return err
		}
		validateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		validation := exec.CommandContext(validateCtx, cfg.Mihomo.BinaryPath, "-t", "-d", runtimeDir)
		platform.ConfigureCoreProcess(validation)
		output, err := validation.CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("Mihomo 新配置校验失败，已保留当前进程: %s", sanitizeLog(string(output)))
		}
	}
	if err := m.stop(); err != nil {
		return err
	}
	return m.start(ctx)
}

// Status 获取进程状态，并在运行时补充当前节点。
func (m *Manager) Status(ctx context.Context) core.Status {
	m.mu.RLock()
	state, lastError, cmd := m.state, m.lastError, m.cmd
	m.mu.RUnlock()
	cfg := m.store.Snapshot()
	status := core.Status{
		Name: "mihomo", Version: cfg.Mihomo.Version, State: state,
		MixedPort: cfg.Mihomo.MixedPort, Mode: cfg.Mihomo.Mode, Error: lastError,
		CurrentNode: cfg.SelectedNode,
	}
	if cmd != nil && cmd.Process != nil && state == core.StateRunning {
		status.PID = cmd.Process.Pid
		client := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey)
		if _, current, err := client.ListNodes(ctx); err == nil && current != "" {
			status.CurrentNode = current
		}
		status.EffectiveNode, _ = client.SelectedProxyNode(ctx)
	}
	return status
}

// ListNodes 返回节点清单。
func (m *Manager) ListNodes(ctx context.Context) ([]core.Node, error) {
	m.mu.RLock()
	state := m.state
	m.mu.RUnlock()
	if state != core.StateRunning {
		return m.cachedNodes(), nil
	}
	cfg := m.store.Snapshot()
	nodes, current, err := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey).ListNodes(ctx)
	if err == nil && current != "" && current != cfg.SelectedNode {
		_ = m.store.Update(func(next *appconfig.Config) error {
			next.SelectedNode = current
			return nil
		})
	}
	return nodes, err
}

// TestNodes 测试所有节点延迟。
func (m *Manager) TestNodes(ctx context.Context) (map[string]int, error) {
	if err := m.requireRunning(); err != nil {
		return nil, err
	}
	cfg := m.store.Snapshot()
	return NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey).TestNodes(ctx)
}

// SelectNode 选择节点并保存状态。
func (m *Manager) SelectNode(ctx context.Context, name string) error {
	if err := m.requireRunning(); err != nil {
		return err
	}
	cfg := m.store.Snapshot()
	if err := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey).SelectNode(ctx, name); err != nil {
		return err
	}
	return m.store.Update(func(next *appconfig.Config) error {
		next.SelectedNode = name
		return nil
	})
}

// UpdateSubscription 要求 Mihomo 立即刷新 provider。
func (m *Manager) UpdateSubscription(ctx context.Context, name string) error {
	m.mu.RLock()
	state := m.state
	m.mu.RUnlock()
	if state != core.StateRunning {
		return fmt.Errorf("Mihomo 未运行（当前状态 %s），无法调用订阅更新接口", state)
	}
	cfg := m.store.Snapshot()
	client := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey)
	for _, sub := range cfg.Subscriptions {
		if strings.EqualFold(sub.Name, name) && strings.EqualFold(sub.UpdateVia, "proxy") {
			if err := client.ValidateProxySelection(ctx); err != nil {
				return err
			}
			break
		}
	}
	previous, _ := client.ProviderSnapshot(ctx, name)
	if err := client.UpdateProvider(ctx, name); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			via := "直连"
			for _, sub := range cfg.Subscriptions {
				if strings.EqualFold(sub.Name, name) && strings.EqualFold(sub.UpdateVia, "proxy") {
					via = "通过 PROXY 策略组"
					break
				}
			}
			return fmt.Errorf("等待 Mihomo 更新订阅超时（当前下载路径：%s）；127.0.0.1 是本地控制接口，不代表订阅正在走代理。请检查订阅服务器、DNS 和防火墙，或用 /logs 200 查看 Mihomo 日志", via)
		}
		return err
	}
	// 不同 Mihomo 版本对 PUT provider 的处理可能是同步或异步。
	// 短暂轮询避免在刷新任务刚刚入队时把它误判为空订阅。
	count, err := waitProviderNodes(ctx, client, name, 12*time.Second, previous.UpdatedAt)
	if err != nil {
		return fmt.Errorf("确认订阅解析结果: %w", err)
	}
	if count == 0 {
		return errors.New("订阅已下载，但 Mihomo 未解析出任何节点；请确认订阅站返回 Clash/Mihomo YAML 或 JSON，并用 /logs 查看解析错误")
	}
	snapshot, err := client.ProviderSnapshot(ctx, name)
	if err != nil {
		return fmt.Errorf("读取更新后的节点信息: %w", err)
	}
	if err := m.saveSnapshot(name, snapshot); err != nil {
		return fmt.Errorf("订阅已更新，但保存离线节点清单失败: %w", err)
	}
	return nil
}

func waitProviderNodes(ctx context.Context, client *Client, name string, timeout time.Duration, previous ...time.Time) (int, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		snapshot, err := client.ProviderSnapshot(waitCtx, name)
		fresh := len(previous) == 0 || previous[0].IsZero() || snapshot.UpdatedAt.After(previous[0])
		if err == nil && len(snapshot.Nodes) > 0 && fresh {
			return len(snapshot.Nodes), nil
		}
		if err == nil && !fresh {
			lastErr = errors.New("订阅更新时间未变化，不能确认新内容已加载；保留原节点，请查看 /logs 后重试")
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if lastErr != nil {
				return 0, lastErr
			}
			return 0, nil
		case <-ticker.C:
		}
	}
}

func (m *Manager) TestSubscription(ctx context.Context, name string) error {
	cfg := m.store.Snapshot()
	client := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey)
	count, err := client.ProviderNodeCount(ctx, name)
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("订阅中没有可供健康检查的节点")
	}
	return client.TestProvider(ctx, name)
}

// SetMode 动态切换模式并持久化。
func (m *Manager) SetMode(ctx context.Context, mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "rule" && mode != "global" && mode != "direct" {
		return errors.New("模式必须是 rule、global 或 direct")
	}
	cfg := m.store.Snapshot()
	if m.Status(ctx).State == core.StateRunning {
		if err := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey).SetMode(ctx, mode); err != nil {
			return err
		}
	}
	return m.store.Update(func(next *appconfig.Config) error {
		next.Mihomo.Mode = mode
		return nil
	})
}

// Logs 返回最近日志。
func (m *Manager) Logs(limit int) []string { return m.logs.Last(limit) }

func (m *Manager) scanLogs(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		m.logs.Add(sanitizeLog(scanner.Text()))
	}
}

func (m *Manager) waitProcess(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	// done 必须在状态清理之后关闭，否则 Restart 会被旧进程的退出回调覆盖。
	defer close(done)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != cmd {
		return
	}
	m.cmd = nil
	m.done = nil
	if m.state == core.StateStopping {
		m.state = core.StateStopped
		m.lastError = ""
		m.logs.Add("Mihomo 已停止")
		return
	}
	if err != nil {
		m.state = core.StateFailed
		m.lastError = "Mihomo 异常退出: " + err.Error()
		m.logs.Add(m.lastError)
	} else {
		m.state = core.StateStopped
	}
}

func (m *Manager) requireRunning() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state != core.StateRunning {
		return errors.New("内核未运行；缓存节点仅供查看，请先 /core start 再选择或测速")
	}
	return nil
}

func (m *Manager) setFailure(err error) {
	m.mu.Lock()
	m.state = core.StateFailed
	m.lastError = err.Error()
	m.mu.Unlock()
	m.logs.Add("错误: " + sanitizeLog(err.Error()))
}

var sensitiveURL = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
var sensitiveParameter = regexp.MustCompile(`(?i)(token|password|secret)=([^&\s"']*)`)

// sanitizeLog 同时处理路径型订阅凭据和重复参数；不能只脱敏第一个 token。
func sanitizeLog(value string) string {
	value = sensitiveURL.ReplaceAllString(value, "[URL 已脱敏]")
	value = sensitiveParameter.ReplaceAllString(value, "$1=***")
	return strings.TrimSpace(value)
}
