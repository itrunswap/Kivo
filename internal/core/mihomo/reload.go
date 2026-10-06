package mihomo

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/itrunswap/Kivo/internal/platform"
)

// Reload 通过控制接口热重载 provider 路径，不创建新的常驻内核进程。
// 失败时恢复磁盘上的旧配置；控制接口的失败仍向上返回，绝不假报应用成功。
func (m *Manager) Reload(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if err := m.requireRunning(); err != nil {
		return err
	}
	cfg := m.store.Snapshot()
	m.mu.RLock()
	options, listen := m.options, m.providerListen
	m.mu.RUnlock()
	if listen != "" {
		cfg.Web.Listen = listen
	}
	dir := filepath.Join(m.store.Paths().RuntimeDir, "mihomo")
	path := filepath.Join(dir, "config.yaml")
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := GenerateConfig(cfg, dir, options); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			if err := os.WriteFile(path, previous, 0o600); err != nil {
				m.logs.Add("恢复旧运行配置失败: " + err.Error())
			}
		}
	}()
	validationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(validationCtx, cfg.Mihomo.BinaryPath, "-t", "-d", dir)
	platform.ConfigureCoreProcess(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("热重载预检失败: %s", sanitizeLog(string(output)))
	}
	client := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey)
	if err := client.withTimeout(60*time.Second).request(ctx, "PUT", "/configs?force=true", map[string]string{"path": path}, nil); err != nil {
		return err
	}
	committed = true
	if cfg.SelectedNode != "" {
		if err := client.SelectNode(ctx, cfg.SelectedNode); err != nil {
			m.logs.Add("热重载后无法恢复保存的节点，使用内核当前选择: " + sanitizeLog(err.Error()))
		}
	}
	if cfg.Mihomo.Mode == "global" {
		if err := client.SetMode(ctx, "global"); err != nil {
			return err
		}
	}
	if err := waitMixedPort(ctx, cfg.Mihomo.MixedPort); err != nil {
		return err
	}
	m.logs.Add("订阅访问路径已热重载，Mihomo 进程保持运行")
	return nil
}

// waitMixedPort 等待监听器就绪。配置接口返回成功不一定意味着异步重建的
// 监听器已经接受连接，立即进行代理检测可能遇到短暂 connection refused。
func waitMixedPort(ctx context.Context, port int) error {
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(waitCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("等待代理端口 %d 就绪: %w", port, waitCtx.Err())
		case <-ticker.C:
		}
	}
}
