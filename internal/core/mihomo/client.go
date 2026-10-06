package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
)

// Client 封装 Mihomo External Controller API。
type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

const providerOperationTimeout = 2 * time.Minute

// NewClient 创建带严格超时的本地控制客户端。
func NewClient(address, secret string) *Client {
	return &Client{
		baseURL: "http://" + address,
		secret:  secret,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// withTimeout 复制客户端并只调整当前操作的超时。Provider 刷新需要
// 等待 Mihomo 访问远程订阅，不应与本地状态查询共用 15 秒的严格时限。
func (c *Client) withTimeout(timeout time.Duration) *Client {
	next := *c
	httpClient := *c.http
	httpClient.Timeout = timeout
	next.http = &httpClient
	return &next
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码 Mihomo 请求: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("创建 Mihomo 请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接 Mihomo 控制接口: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Mihomo 返回 HTTP %d: %s", resp.StatusCode, sanitizeLog(string(limited)))
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
			return fmt.Errorf("解析 Mihomo 响应: %w", err)
		}
	}
	return nil
}

// Healthy 判断控制接口是否已经可用。
func (c *Client) Healthy(ctx context.Context) bool {
	var result struct {
		Version string `json:"version"`
	}
	return c.request(ctx, http.MethodGet, "/version", nil, &result) == nil
}

type proxyInfo struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Alive        bool     `json:"alive"`
	UDP          bool     `json:"udp"`
	ProviderName string   `json:"provider-name"`
	Now          string   `json:"now"`
	All          []string `json:"all"`
	History      []struct {
		Delay int `json:"delay"`
	} `json:"history"`
}

type providerInfo struct {
	Name        string      `json:"name"`
	VehicleType string      `json:"vehicleType"`
	UpdatedAt   string      `json:"updatedAt"`
	Proxies     []proxyInfo `json:"proxies"`
}

// providerCatalog 读取 Mihomo 已加载的全部 Proxy Provider。与策略组的
// all 字段相比，这个接口能直接给出每个订阅的解析结果和节点归属。
func (c *Client) providerCatalog(ctx context.Context) (map[string]providerInfo, error) {
	var result struct {
		Providers map[string]providerInfo `json:"providers"`
	}
	if err := c.request(ctx, http.MethodGet, "/providers/proxies", nil, &result); err != nil {
		return nil, err
	}
	return result.Providers, nil
}

// ProviderNodeCount 确认指定 provider 最终被 Mihomo 解析出的节点数。
// provider 名称按不区分大小写匹配，但返回时仍保留 Mihomo 中的原始名称。
func (c *Client) ProviderNodeCount(ctx context.Context, name string) (int, error) {
	providers, err := c.providerCatalog(ctx)
	if err != nil {
		return 0, err
	}
	for providerName, provider := range providers {
		if strings.EqualFold(providerName, name) || strings.EqualFold(provider.Name, name) {
			return len(provider.Proxies), nil
		}
	}
	return 0, fmt.Errorf("Mihomo 中不存在订阅 provider %s", name)
}

// ListNodes 返回 PROXY 策略组中的真实节点。
func (c *Client) ListNodes(ctx context.Context) ([]core.Node, string, error) {
	var group proxyInfo
	if err := c.request(ctx, http.MethodGet, "/proxies/PROXY", nil, &group); err != nil {
		return nil, "", err
	}
	var all struct {
		Proxies map[string]proxyInfo `json:"proxies"`
	}
	if err := c.request(ctx, http.MethodGet, "/proxies", nil, &all); err != nil {
		return nil, "", err
	}
	providers, providerErr := c.providerCatalog(ctx)
	nodes := make([]core.Node, 0, len(group.All))
	seen := map[string]struct{}{}
	appendNode := func(name, providerName string, info proxyInfo) {
		if isInternalProxy(name, info.Type) {
			return
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		if live, ok := all.Proxies[name]; ok {
			info = live
		}
		if info.ProviderName == "" {
			info.ProviderName = providerName
		}
		delay := 0
		if len(info.History) > 0 {
			delay = info.History[len(info.History)-1].Delay
		}
		nodes = append(nodes, core.Node{
			Name: name, Type: info.Type, Alive: info.Alive, Delay: delay, Tested: len(info.History) > 0,
			ProviderName: info.ProviderName, UDP: info.UDP,
		})
	}
	if providerErr == nil {
		for providerName, provider := range providers {
			for _, info := range provider.Proxies {
				appendNode(info.Name, providerName, info)
			}
		}
	} else {
		// 兼容旧版 Mihomo：provider 列表接口不可用时回退到 PROXY 组。
		for _, name := range group.All {
			if info, ok := all.Proxies[name]; ok {
				appendNode(name, info.ProviderName, info)
			}
		}
	}
	// 序号用于后续选择，不能随健康检查延迟或 map 遍历顺序改变。
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes, group.Now, nil
}

func isInternalProxy(name, proxyType string) bool {
	if name == "DIRECT" || name == "REJECT" || name == "AUTO" {
		return true
	}
	switch proxyType {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Compatible":
		return true
	default:
		return false
	}
}

// TestNodes 测试 PROXY 组中的所有节点延迟。
func (c *Client) TestNodes(ctx context.Context) (map[string]int, error) {
	nodes, _, err := c.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("url", "https://www.gstatic.com/generate_204")
	query.Set("timeout", "5000")
	query.Set("expected", "204")
	result := map[string]int{}
	if err := c.withTimeout(providerOperationTimeout).request(ctx, http.MethodGet, "/group/PROXY/delay?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}
	// Mihomo 会省略失败项，并可能返回 DIRECT/AUTO。只保留真实节点，
	// 以 0 明确表示本次未通过，不能保留上一次成功的延迟或漏报失败数。
	checked := make(map[string]int, len(nodes))
	for _, node := range nodes {
		checked[node.Name] = max(result[node.Name], 0)
	}
	return checked, nil
}

// SelectNode 修改 PROXY 策略组的固定选择。
func (c *Client) SelectNode(ctx context.Context, name string) error {
	return c.request(ctx, http.MethodPut, "/proxies/PROXY", map[string]string{"name": name}, nil)
}

// UpdateProvider 立即刷新指定订阅。
func (c *Client) UpdateProvider(ctx context.Context, name string) error {
	return c.withTimeout(providerOperationTimeout).request(ctx, http.MethodPut, "/providers/proxies/"+url.PathEscape(name), nil, nil)
}

// TestProvider 触发指定 Proxy Provider 的全部节点健康检查。
func (c *Client) TestProvider(ctx context.Context, name string) error {
	return c.withTimeout(providerOperationTimeout).request(ctx, http.MethodGet, "/providers/proxies/"+url.PathEscape(name)+"/healthcheck", nil, nil)
}

// SetMode 动态切换 rule/global/direct 模式。
func (c *Client) SetMode(ctx context.Context, mode string) error {
	if mode == "global" {
		// GLOBAL 是 Mihomo 独立的选择组，默认可能选 DIRECT；仅 PATCH mode
		// 不会自动沿用用户在 PROXY 中选择的节点。
		if err := c.request(ctx, http.MethodPut, "/proxies/GLOBAL", map[string]string{"name": "PROXY"}, nil); err != nil {
			return fmt.Errorf("设置全局代理出口: %w", err)
		}
	}
	return c.request(ctx, http.MethodPatch, "/configs", map[string]string{"mode": mode}, nil)
}
