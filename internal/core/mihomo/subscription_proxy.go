package mihomo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/itrunswap/Kivo/internal/core"
)

// RuntimeOptions 是每次启动生成的内部参数，不写入用户配置或公开状态接口。
type RuntimeOptions struct {
	SubscriptionPort   int
	SubscriptionSecret string
}

func newRuntimeOptions() (RuntimeOptions, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return RuntimeOptions{}, fmt.Errorf("分配内部订阅代理端口: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return RuntimeOptions{}, err
	}
	return RuntimeOptions{SubscriptionPort: port, SubscriptionSecret: hex.EncodeToString(secret[:])}, nil
}

// SubscriptionProxyURL 不改变全局模式；DIRECT 模式下也按显式 PROXY 出站。
// 没有实际节点时直接报错，不偷偷退回直连。
func (m *Manager) SubscriptionProxyURL(ctx context.Context) (*url.URL, error) {
	m.mu.RLock()
	option, state := m.options, m.state
	m.mu.RUnlock()
	// 启动时 provider 可能已经开始拉取，此时允许通过已就绪的控制接口查询。
	if (state != core.StateRunning && state != core.StateStarting) || option.SubscriptionPort == 0 {
		return nil, errors.New("订阅代理入口尚未就绪，请先启动内核；首次获取节点可使用 --direct")
	}
	cfg := m.store.Snapshot()
	if err := NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey).ValidateProxySelection(ctx); err != nil {
		return nil, err
	}
	return &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(option.SubscriptionPort), User: url.UserPassword("kivo", option.SubscriptionSecret)}, nil
}

// ValidateProxySelection 展开策略组，拒绝 DIRECT、REJECT、空组和循环引用。
func (c *Client) ValidateProxySelection(ctx context.Context) error {
	_, err := c.SelectedProxyNode(ctx)
	return err
}

// SelectedProxyNode 解析 PROXY/AUTO 当前实际节点，供联网检测识别切换后的过期结果。
func (c *Client) SelectedProxyNode(ctx context.Context) (string, error) {
	name := "PROXY"
	seen := make(map[string]bool)
	// Provider 节点未必在 /proxies/<name> 注册（部分版本只注册策略组）。
	// 先从 provider 目录建立叶子节点映射，不能把 404 当成没有可用节点。
	leaves := make(map[string]proxyInfo)
	if providers, err := c.providerCatalog(ctx); err == nil {
		for _, provider := range providers {
			for _, proxy := range provider.Proxies {
				leaves[proxy.Name] = proxy
			}
		}
	}
	for depth := 0; depth < 16; depth++ {
		if seen[name] {
			break
		}
		seen[name] = true
		proxy, leaf := leaves[name]
		if !leaf || name == "PROXY" || name == "AUTO" {
			if err := c.request(ctx, "GET", "/proxies/"+url.PathEscape(name), nil, &proxy); err != nil {
				return "", errors.New("无法确认 PROXY 出站节点，请先启动内核并选择可用节点，或使用 --direct")
			}
		}
		switch strings.ToLower(proxy.Type) {
		case "direct", "reject", "rejectdrop", "compatible", "pass", "":
			return "", errors.New("PROXY 当前没有实际代理节点（可能选中了 DIRECT）；请先 /node select 选择节点，首次更新可使用 --direct")
		case "selector", "urltest", "fallback", "loadbalance", "relay":
			if proxy.Now == "" {
				return "", errors.New("PROXY 策略组暂无可用出站，请先使用 --direct 获取节点")
			}
			name = proxy.Now
		default:
			return name, nil
		}
	}
	return "", errors.New("PROXY 策略组引用异常，无法安全执行代理更新")
}
