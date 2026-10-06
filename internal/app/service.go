// Package app 实现 CLI、Web 和普通子命令共用的业务用例。
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

// Service 是应用层唯一入口，保证 Web 和 CLI 行为一致。
type Service struct {
	checkMu            sync.Mutex
	connectivityMu     sync.RWMutex
	lastConnectivity   *ConnectivityReport
	connectivityKey    [32]byte
	probeTargets       []probeTarget
	subscriptionAction sync.Mutex // CLI/Web 共享互斥，防止并发更新改写下载路径或误停临时内核。
	downloadMu         sync.Mutex
	lastDownload       subscriptionDownloadResult // 只保存最近一次安全错误，不缓存解密内容。
	store              *config.Store
	core               core.Adapter
	systemProxy        platform.SystemProxyBackend
}

// NewService 创建业务服务。
func NewService(store *config.Store, adapter core.Adapter) *Service {
	return NewServiceWithSystemProxy(store, adapter, platform.NewSystemProxyBackend())
}

// NewServiceWithSystemProxy 允许平台适配器注入，也用于不修改真实系统设置的事务测试。
func NewServiceWithSystemProxy(store *config.Store, adapter core.Adapter, backend platform.SystemProxyBackend) *Service {
	return &Service{store: store, core: adapter, systemProxy: backend, probeTargets: defaultProbeTargets()}
}

// ProxySetup 返回服务所在主机的系统/浏览器接入指引，不执行系统配置写入。
func (s *Service) ProxySetup() platform.ProxyGuide {
	return platform.SystemProxyGuide(s.store.Snapshot().Mihomo.MixedPort)
}

// Overview 是控制台首页需要的聚合数据。
type Overview struct {
	Connection         ConnectionSummary          `json:"connection"`
	Connectivity       *ConnectivityReport        `json:"connectivity,omitempty"`
	Core               core.Status                `json:"core"`
	SubscriptionCount  int                        `json:"subscriptionCount"`
	EnabledCount       int                        `json:"enabledCount"`
	Platform           string                     `json:"platform"`
	Architecture       string                     `json:"architecture"`
	WebAddress         string                     `json:"webAddress"`
	TUNEnabled         bool                       `json:"tunEnabled"`
	ProxyPortListening bool                       `json:"proxyPortListening"`
	SystemProxy        platform.SystemProxyStatus `json:"systemProxy"`
}

// Overview 返回首页状态。
func (s *Service) Overview(ctx context.Context) Overview {
	cfg := s.store.Snapshot()
	enabled := 0
	for _, sub := range cfg.Subscriptions {
		if sub.Enabled {
			enabled++
		}
	}
	status := s.core.Status(ctx)
	address := fmt.Sprintf("127.0.0.1:%d", cfg.Mihomo.MixedPort)
	listening := false
	if status.State == core.StateRunning {
		dialer := net.Dialer{Timeout: 200 * time.Millisecond}
		if connection, err := dialer.DialContext(ctx, "tcp", address); err == nil {
			listening = true
			connection.Close()
		}
	}
	overview := Overview{
		Core: status, SubscriptionCount: len(cfg.Subscriptions), EnabledCount: enabled,
		Platform: runtime.GOOS, Architecture: runtime.GOARCH, WebAddress: cfg.Web.Listen,
		TUNEnabled:         cfg.Mihomo.TUNEnabled,
		ProxyPortListening: listening, SystemProxy: s.SystemProxyStatus(ctx),
	}
	overview.Connectivity = s.cachedConnectivity(status)
	overview.Connection = SummarizeConnection(overview)
	return overview
}

// PublicSubscription 是移除敏感凭据后的订阅信息。
type PublicSubscription struct {
	Index            int    `json:"index"`
	Name             string `json:"name"`
	URL              string `json:"url"`
	AuthType         string `json:"authType"`
	UpdateVia        string `json:"updateVia"`
	UpdateInterval   int    `json:"updateInterval"`
	HealthInterval   int    `json:"healthInterval"`
	HealthCheckURL   string `json:"healthCheckURL"`
	Enabled          bool   `json:"enabled"`
	Group            string `json:"group"`
	AdditionalPrefix string `json:"additionalPrefix,omitempty"`
}

// SubscriptionInput 是创建订阅的输入模型。
type SubscriptionInput struct {
	Name             string `json:"name"`
	URL              string `json:"url"`
	AuthType         string `json:"authType"`
	Username         string `json:"username,omitempty"`
	Secret           string `json:"secret,omitempty"`
	UpdateVia        string `json:"updateVia,omitempty"`
	UpdateInterval   int    `json:"updateInterval"`
	HealthInterval   int    `json:"healthInterval"`
	HealthCheckURL   string `json:"healthCheckURL"`
	AdditionalPrefix string `json:"additionalPrefix,omitempty"`
	Group            string `json:"group,omitempty"`
}

// ListSubscriptions 返回脱敏订阅清单。
func (s *Service) ListSubscriptions() []PublicSubscription {
	cfg := s.store.Snapshot()
	result := make([]PublicSubscription, 0, len(cfg.Subscriptions))
	for index, sub := range cfg.Subscriptions {
		result = append(result, PublicSubscription{
			Index: index + 1, Name: sub.Name, URL: redactURL(sub.URL), AuthType: defaultString(sub.Auth.Type, "none"),
			UpdateVia:      defaultString(sub.UpdateVia, "direct"),
			UpdateInterval: sub.UpdateInterval, HealthInterval: sub.HealthInterval,
			HealthCheckURL: sub.HealthCheckURL, Enabled: sub.Enabled, Group: sub.Group,
			AdditionalPrefix: sub.AdditionalPrefix,
		})
	}
	return result
}

// AddSubscription 校验、保存订阅，并在内核运行时安全重启以加载 provider。
func (s *Service) AddSubscription(ctx context.Context, input SubscriptionInput) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	input.Name = strings.TrimSpace(input.Name)
	autoNamed := input.Name == ""
	input.URL = strings.TrimSpace(input.URL)
	if input.URL == "" {
		return errors.New("订阅地址不能为空")
	}
	parsed, err := url.ParseRequestURI(input.URL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("订阅地址必须是有效的 HTTP 或 HTTPS URL")
	}
	if input.Name == "" {
		input.Name = parsed.Hostname()
		if input.Name == "" {
			input.Name = "subscription"
		}
	}
	authType := strings.ToLower(strings.TrimSpace(input.AuthType))
	if authType == "" {
		authType = "none"
	}
	if !slices.Contains([]string{"none", "basic", "bearer", "token", "age", "aes"}, authType) {
		return errors.New("不支持的订阅认证方式")
	}
	if authType != "none" && strings.TrimSpace(input.Secret) == "" {
		return errors.New("所选认证方式需要密码或 Token")
	}
	input.UpdateVia = strings.ToLower(strings.TrimSpace(input.UpdateVia))
	if input.UpdateVia == "" {
		input.UpdateVia = "direct"
	}
	if input.UpdateVia != "direct" && input.UpdateVia != "proxy" {
		return errors.New("订阅更新路径必须是 direct 或 proxy")
	}
	if input.UpdateInterval <= 0 {
		input.UpdateInterval = 3600
	}
	if input.HealthInterval <= 0 {
		input.HealthInterval = 300
	}
	if input.HealthCheckURL == "" {
		input.HealthCheckURL = "https://www.gstatic.com/generate_204"
	}
	input.Group = strings.TrimSpace(input.Group)
	if input.Group == "" {
		input.Group = "default"
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		groupFound := false
		for _, group := range cfg.SubscriptionGroups {
			if strings.EqualFold(group.Name, input.Group) {
				input.Group = group.Name
				groupFound = true
				break
			}
		}
		if !groupFound {
			return fmt.Errorf("订阅分组 %s 不存在", input.Group)
		}
		if autoNamed {
			base := input.Name
			for suffix := 2; ; suffix++ {
				duplicate := slices.ContainsFunc(cfg.Subscriptions, func(sub config.Subscription) bool { return strings.EqualFold(sub.Name, input.Name) })
				if !duplicate {
					break
				}
				input.Name = fmt.Sprintf("%s-%d", base, suffix)
			}
		}
		if input.AdditionalPrefix == "" {
			input.AdditionalPrefix = "[" + input.Name + "] "
		}
		for _, sub := range cfg.Subscriptions {
			if strings.EqualFold(sub.Name, input.Name) {
				return fmt.Errorf("订阅 %s 已存在", input.Name)
			}
		}
		cfg.Subscriptions = append(cfg.Subscriptions, config.Subscription{
			Name: input.Name, URL: input.URL,
			Auth:           config.SubscriptionAuth{Type: authType, Username: input.Username, Secret: input.Secret},
			UpdateVia:      input.UpdateVia,
			UpdateInterval: input.UpdateInterval, HealthInterval: input.HealthInterval,
			HealthCheckURL: input.HealthCheckURL, Enabled: true, Group: input.Group,
			AdditionalPrefix: input.AdditionalPrefix,
		})
		return nil
	}); err != nil {
		return err
	}
	return s.reloadIfRunning(ctx)
}

// RemoveSubscription 删除订阅并重载内核。
func (s *Service) RemoveSubscription(ctx context.Context, name string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	found := false
	if err := s.store.Update(func(cfg *config.Config) error {
		filtered := cfg.Subscriptions[:0]
		for _, sub := range cfg.Subscriptions {
			if sub.Name == name {
				found = true
				continue
			}
			filtered = append(filtered, sub)
		}
		cfg.Subscriptions = filtered
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("订阅 %s 不存在", name)
	}
	return s.reloadIfRunning(ctx)
}

// UpdateSubscription 立即刷新订阅。
func (s *Service) UpdateSubscription(ctx context.Context, name string) error {
	started := time.Now()
	err := s.core.UpdateSubscription(ctx, name)
	if err != nil {
		s.downloadMu.Lock()
		last := s.lastDownload
		s.downloadMu.Unlock()
		if strings.EqualFold(last.name, name) && last.finishedAt.After(started) && last.err != nil {
			return fmt.Errorf("AES 订阅下载/解密失败（%s）: %w", last.via, last.err)
		}
	}
	return err
}

// InstallCore 安装 Mihomo。
func (s *Service) InstallCore(ctx context.Context, version string, progress func(core.InstallEvent)) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.protectProxyAfterCoreChange(ctx, func() error { return s.core.Install(ctx, version, progress) })
}

func (s *Service) StartCore(ctx context.Context) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("订阅操作正在执行，请完成后再启动内核")
	}
	defer s.subscriptionAction.Unlock()
	if err := s.protectProxyAfterCoreChange(ctx, func() error { return s.core.Start(ctx) }); err != nil {
		return err
	}
	return s.store.Update(func(cfg *config.Config) error {
		cfg.Mihomo.AutoStart = true
		return nil
	})
}
func (s *Service) StopCore(ctx context.Context) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("订阅操作正在执行，请完成后再停止内核")
	}
	defer s.subscriptionAction.Unlock()
	if _, err := s.restoreSystemProxyLocked(ctx, false); err != nil {
		return err
	}
	if err := s.core.Stop(ctx); err != nil {
		return err
	}
	return s.store.Update(func(cfg *config.Config) error {
		cfg.Mihomo.AutoStart = false
		cfg.SystemProxy.AutoConnect = false
		return nil
	})
}
func (s *Service) RestartCore(ctx context.Context) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("订阅操作正在执行，请完成后再重启内核")
	}
	defer s.subscriptionAction.Unlock()
	if err := s.protectProxyAfterCoreChange(ctx, func() error { return s.core.Restart(ctx) }); err != nil {
		return err
	}
	return s.store.Update(func(cfg *config.Config) error {
		cfg.Mihomo.AutoStart = true
		return nil
	})
}
func (s *Service) CoreStatus(ctx context.Context) core.Status         { return s.core.Status(ctx) }
func (s *Service) ListNodes(ctx context.Context) ([]core.Node, error) { return s.core.ListNodes(ctx) }
func (s *Service) TestNodes(ctx context.Context) (map[string]int, error) {
	return s.core.TestNodes(ctx)
}
func (s *Service) SelectNode(ctx context.Context, name string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("订阅操作正在执行，请完成后再切换节点")
	}
	defer s.subscriptionAction.Unlock()
	return s.core.SelectNode(ctx, name)
}
func (s *Service) SetMode(ctx context.Context, mode string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	if err := s.core.SetMode(ctx, mode); err != nil {
		return err
	}
	return s.store.Update(func(cfg *config.Config) error {
		if mode == "global" || mode == "direct" {
			cfg.Routing.ActiveProfile = mode
		} else if cfg.Routing.ActiveProfile == "global" || cfg.Routing.ActiveProfile == "direct" {
			cfg.Routing.ActiveProfile = "rule"
		}
		return nil
	})
}
func (s *Service) Logs(limit int) []string { return s.core.Logs(limit) }

// Settings 是允许通过控制台修改的非敏感设置。
type Settings struct {
	Listen        string `json:"listen"`
	MixedPort     int    `json:"mixedPort"`
	Mode          string `json:"mode"`
	AllowLAN      bool   `json:"allowLAN"`
	TUNEnabled    bool   `json:"tunEnabled"`
	DownloadProxy string `json:"downloadProxy"`
	DownloadRetry int    `json:"downloadRetry"`
}

// WebSecurity 是可公开返回的 Web 鉴权状态，不包含实际 Token。
type WebSecurity struct {
	AuthEnabled bool `json:"authEnabled"`
}

// GetWebSecurity 返回 Web 是否启用了 Token 鉴权。
func (s *Service) GetWebSecurity() WebSecurity {
	return WebSecurity{AuthEnabled: strings.TrimSpace(s.store.Snapshot().Web.Secret) != ""}
}

// UpdateWebToken 更新 Web Token。空字符串表示关闭鉴权，变更保存后对下一次请求立即生效。
func (s *Service) UpdateWebToken(token string) (WebSecurity, error) {
	token = strings.TrimSpace(token)
	if err := s.store.Update(func(cfg *config.Config) error {
		cfg.Web.Secret = token
		return nil
	}); err != nil {
		return WebSecurity{}, err
	}
	return WebSecurity{AuthEnabled: token != ""}, nil
}

// GetSettings 返回公开设置。
func (s *Service) GetSettings() Settings {
	cfg := s.store.Snapshot()
	return Settings{Listen: cfg.Web.Listen, MixedPort: cfg.Mihomo.MixedPort, Mode: cfg.Mihomo.Mode, AllowLAN: cfg.Mihomo.AllowLAN, TUNEnabled: cfg.Mihomo.TUNEnabled, DownloadProxy: cfg.Mihomo.DownloadProxy, DownloadRetry: cfg.Mihomo.DownloadRetry}
}

// UpdateSettings 更新设置。涉及端口或 TUN 的变更会重启运行中的内核。
func (s *Service) UpdateSettings(ctx context.Context, settings Settings) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行，请稍后修改设置")
	}
	defer s.subscriptionAction.Unlock()
	current := s.store.Snapshot()
	if settings.DownloadRetry == 0 {
		settings.DownloadRetry = current.Mihomo.DownloadRetry
	}
	if settings.MixedPort < 1 || settings.MixedPort > 65535 {
		return errors.New("代理端口必须在 1-65535 之间")
	}
	if settings.Mode != "rule" && settings.Mode != "global" && settings.Mode != "direct" {
		return errors.New("模式必须是 rule、global 或 direct")
	}
	if settings.DownloadRetry < 1 || settings.DownloadRetry > 10 {
		return errors.New("downloadRetry 必须在 1-10 之间")
	}
	if address := strings.TrimSpace(settings.DownloadProxy); address != "" && !strings.EqualFold(address, "system") {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("downloadProxy 必须是有效的 http:// 或 https:// 地址")
		}
	}
	needsReload := current.Mihomo.MixedPort != settings.MixedPort || current.Mihomo.Mode != settings.Mode || current.Mihomo.AllowLAN != settings.AllowLAN || current.Mihomo.TUNEnabled != settings.TUNEnabled
	moveProxy := current.SystemProxy.Lease != nil && current.Mihomo.MixedPort != settings.MixedPort
	if moveProxy {
		result, err := s.restoreSystemProxyLocked(ctx, false)
		if err != nil {
			return err
		}
		if result.Warning {
			return errors.New("代理设置存在外部修改，暂不变更端口；请先 /system-proxy recover")
		}
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		cfg.Mihomo.MixedPort = settings.MixedPort
		cfg.Mihomo.Mode = settings.Mode
		// Web 设置与 /mode 共享同一语义：Global/Direct 使用同名路由配置，
		// 从这两种模式切回 Rule 时恢复基础 rule 配置，不覆盖其他自定义配置。
		if settings.Mode == "global" || settings.Mode == "direct" {
			cfg.Routing.ActiveProfile = settings.Mode
		} else if cfg.Routing.ActiveProfile == "global" || cfg.Routing.ActiveProfile == "direct" {
			cfg.Routing.ActiveProfile = "rule"
		}
		cfg.Mihomo.AllowLAN = settings.AllowLAN
		cfg.Mihomo.TUNEnabled = settings.TUNEnabled
		cfg.Mihomo.DownloadProxy = strings.TrimSpace(settings.DownloadProxy)
		cfg.Mihomo.DownloadRetry = settings.DownloadRetry
		return nil
	}); err != nil {
		return err
	}
	if needsReload {
		if err := s.reloadIfRunning(ctx); err != nil {
			if moveProxy {
				return errors.Join(err, s.saveProxyPreference(false))
			}
			return err
		}
		if moveProxy && s.core.Status(ctx).State == core.StateRunning {
			_, err := s.enableSystemProxyLocked(ctx, ConnectOptions{SkipCheck: true})
			return err
		}
	}
	return nil
}

// DoctorItem 是诊断结果的一项。
type DoctorItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Doctor 执行只读诊断，不修改系统状态。
func (s *Service) Doctor(ctx context.Context) []DoctorItem {
	cfg := s.store.Snapshot()
	items := []DoctorItem{}
	if cfg.Mihomo.BinaryPath == "" {
		items = append(items, DoctorItem{"Mihomo 内核", "error", "尚未安装"})
	} else if _, err := os.Stat(cfg.Mihomo.BinaryPath); err != nil {
		items = append(items, DoctorItem{"Mihomo 内核", "error", "配置的可执行文件不存在"})
	} else {
		items = append(items, DoctorItem{"Mihomo 内核", "ok", cfg.Mihomo.Version})
	}
	if len(cfg.Subscriptions) == 0 {
		items = append(items, DoctorItem{"订阅", "warning", "尚未添加订阅"})
	} else {
		items = append(items, DoctorItem{"订阅", "ok", fmt.Sprintf("已配置 %d 个订阅", len(cfg.Subscriptions))})
	}
	if canListen(cfg.Mihomo.Controller) {
		items = append(items, DoctorItem{"控制端口", "ok", cfg.Mihomo.Controller + " 可用"})
	} else if s.core.Status(ctx).State == core.StateRunning {
		items = append(items, DoctorItem{"控制端口", "ok", "由运行中的 Mihomo 占用"})
	} else {
		items = append(items, DoctorItem{"控制端口", "error", cfg.Mihomo.Controller + " 已被其他程序占用"})
	}
	if canListen(fmt.Sprintf("127.0.0.1:%d", cfg.Mihomo.MixedPort)) {
		items = append(items, DoctorItem{"代理端口", "ok", fmt.Sprintf("%d 可用", cfg.Mihomo.MixedPort)})
	} else if s.core.Status(ctx).State == core.StateRunning {
		items = append(items, DoctorItem{"代理端口", "ok", "由运行中的 Mihomo 占用"})
	} else {
		items = append(items, DoctorItem{"代理端口", "error", fmt.Sprintf("%d 已被占用", cfg.Mihomo.MixedPort)})
	}
	if cfg.Mihomo.TUNEnabled {
		items = append(items, DoctorItem{"TUN 权限", "warning", "TUN 需要管理员/root 或 CAP_NET_ADMIN 权限，请在目标平台验证"})
	}
	return items
}

func (s *Service) reloadIfRunning(ctx context.Context) error {
	if s.core.Status(ctx).State == core.StateRunning {
		return s.protectProxyAfterCoreChange(ctx, func() error { return s.core.Restart(ctx) })
	}
	return nil
}

// 内核升级/切换/重启若导致端口消失，及时恢复系统设置；恢复失败保留日志供显式修复。
// 调用方负责串行化内核操作，清理使用独立 context，不受原请求取消影响。
func (s *Service) protectProxyAfterCoreChange(ctx context.Context, change func() error) error {
	err := change()
	if s.store.Snapshot().SystemProxy.Lease == nil {
		return err
	}
	if err != nil {
		// 下载/安装失败不一定影响原内核；原入口仍正常时不打断用户现有连接。
		probe, cancel := context.WithTimeout(context.Background(), time.Second)
		ready := s.waitProxyPort(probe) == nil
		cancel()
		if ready {
			return err
		}
	}
	if err == nil {
		err = s.waitProxyPort(ctx)
	}
	if err == nil {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, restoreErr := s.restoreSystemProxyLocked(cleanup, false)
	preferenceErr := s.saveProxyPreference(false)
	return errors.Join(err, restoreErr, preferenceErr)
}

func canListen(address string) bool {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	if parsed.User != nil {
		parsed.User = url.User("***")
	}
	// 订阅凭据也常放在 /link/<token>、/s/<token> 路径，而不只在 query。
	// 公开接口隐藏长标识符及敏感路径键的下一段；实际下载仍使用原 URL。
	parts := strings.Split(parsed.Path, "/")
	for i := range parts {
		previous := ""
		if i > 0 {
			previous = strings.ToLower(parts[i-1])
		}
		if len(parts[i]) >= 20 || previous == "link" || previous == "token" || previous == "subscribe" || previous == "s" {
			if parts[i] != "" {
				parts[i] = "***"
			}
		}
	}
	parsed.Path = strings.Join(parts, "/")
	parsed.RawPath = ""
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "pass") || strings.Contains(lower, "key") || strings.Contains(lower, "secret") {
			query.Set(key, "***")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// WithTimeout 为 CLI 操作提供统一超时。
func WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}
