// Package client 是 CLI 访问本地 Kivo 控制服务的类型安全客户端。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

// Client 连接本机或远程 Kivo 控制 API。
type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

// New 创建客户端。
func New(address, secret string) *Client {
	return &Client{baseURL: "http://" + address, secret: secret, http: &http.Client{Timeout: 30 * time.Second}}
}

// Healthy 检查服务器是否存在，不要求鉴权。
func (c *Client) Healthy(ctx context.Context) bool {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/health", nil)
	response, err := c.http.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// Verify 检查当前客户端密钥是否属于目标控制服务。
// 健康接口是公开的，不能单独用于判断端口上的进程是否为当前数据目录对应的实例。
func (c *Client) Verify(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/v1/session/verify", map[string]any{}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("连接 Kivo 服务: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&envelope); err != nil && response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("解析服务响应: %w", err)
	}
	// 批量操作可能部分失败，错误响应中的已完成结果也必须交给调用方展示。
	if output != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			return err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if envelope.Error != nil {
			return fmt.Errorf("%s", envelope.Error.Message)
		}
		return fmt.Errorf("服务返回 HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *Client) withTimeout(timeout time.Duration) *Client {
	next, httpClient := *c, *c.http
	httpClient.Timeout = timeout
	next.http = &httpClient
	return &next
}

func (c *Client) Overview(ctx context.Context) (app.Overview, error) {
	var out app.Overview
	err := c.do(ctx, http.MethodGet, "/api/v1/overview", nil, &out)
	return out, err
}

func (c *Client) CheckConnectivity(ctx context.Context) (app.ConnectivityReport, error) {
	var report app.ConnectivityReport
	err := c.do(ctx, http.MethodPost, "/api/v1/connectivity/check", map[string]any{}, &report)
	return report, err
}

func (c *Client) ProxySetup(ctx context.Context) (platform.ProxyGuide, error) {
	var guide platform.ProxyGuide
	err := c.do(ctx, http.MethodGet, "/api/v1/proxy/setup", nil, &guide)
	return guide, err
}

func (c *Client) Connect(ctx context.Context, options app.ConnectOptions) (app.ProxyOperationResult, error) {
	var result app.ProxyOperationResult
	err := c.withTimeout(90*time.Second).do(ctx, http.MethodPost, "/api/v1/connection/connect", options, &result)
	return result, err
}

func (c *Client) Disconnect(ctx context.Context) (app.ProxyOperationResult, error) {
	var result app.ProxyOperationResult
	err := c.do(ctx, http.MethodPost, "/api/v1/connection/disconnect", map[string]any{}, &result)
	return result, err
}

func (c *Client) SystemProxyStatus(ctx context.Context) (platform.SystemProxyStatus, error) {
	var result platform.SystemProxyStatus
	err := c.do(ctx, http.MethodGet, "/api/v1/system-proxy", nil, &result)
	return result, err
}

func (c *Client) SystemProxyAction(ctx context.Context, action string, options app.ConnectOptions, force bool) (app.ProxyOperationResult, error) {
	var result app.ProxyOperationResult
	err := c.withTimeout(75*time.Second).do(ctx, http.MethodPost, "/api/v1/system-proxy/"+action, map[string]any{"replace": options.Replace, "adopt": options.Adopt, "force": force}, &result)
	return result, err
}
func (c *Client) Subscriptions(ctx context.Context) ([]app.PublicSubscription, error) {
	var out []app.PublicSubscription
	err := c.do(ctx, http.MethodGet, "/api/v1/subscriptions", nil, &out)
	return out, err
}
func (c *Client) AddSubscription(ctx context.Context, input app.SubscriptionInput) error {
	return c.do(ctx, http.MethodPost, "/api/v1/subscriptions", input, nil)
}
func (c *Client) RemoveSubscription(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/subscriptions?name="+url.QueryEscape(name), nil, nil)
}
func (c *Client) PatchSubscription(ctx context.Context, patch app.SubscriptionPatch) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/subscriptions", patch, nil)
}
func (c *Client) UpdateSubscription(ctx context.Context, name string) error {
	_, err := c.UpdateSubscriptions(ctx, app.SubscriptionUpdateInput{Reference: name})
	return err
}
func (c *Client) UpdateSubscriptions(ctx context.Context, input app.SubscriptionUpdateInput) (app.SubscriptionUpdateResult, error) {
	var result app.SubscriptionUpdateResult
	err := c.withTimeout(6*time.Minute).do(ctx, http.MethodPost, "/api/v1/subscriptions/update", input, &result)
	return result, err
}
func (c *Client) TestSubscription(ctx context.Context, name string) error {
	_, err := c.TestSubscriptions(ctx, app.SubscriptionTestInput{Reference: name})
	return err
}
func (c *Client) TestSubscriptions(ctx context.Context, input app.SubscriptionTestInput) (app.SubscriptionTestResult, error) {
	var result app.SubscriptionTestResult
	err := c.withTimeout(6*time.Minute).do(ctx, http.MethodPost, "/api/v1/subscriptions/test", input, &result)
	return result, err
}
func (c *Client) Nodes(ctx context.Context) ([]core.Node, error) {
	var out []core.Node
	err := c.do(ctx, http.MethodGet, "/api/v1/nodes", nil, &out)
	return out, err
}
func (c *Client) TestNodes(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	err := c.do(ctx, http.MethodPost, "/api/v1/nodes/test", map[string]any{}, &out)
	return out, err
}
func (c *Client) SelectNode(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/nodes/select", map[string]string{"name": name}, nil)
}
func (c *Client) InstallCore(ctx context.Context, version string) ([]core.InstallEvent, error) {
	var out []core.InstallEvent
	err := c.withTimeout(20*time.Minute).do(ctx, http.MethodPost, "/api/v1/core/install", map[string]string{"version": version}, &out)
	return out, err
}
func (c *Client) CoreStatus(ctx context.Context) (core.Status, error) {
	var out core.Status
	err := c.do(ctx, http.MethodGet, "/api/v1/core/status", nil, &out)
	return out, err
}
func (c *Client) CoreInstallations(ctx context.Context) ([]core.Installation, error) {
	var out []core.Installation
	err := c.do(ctx, http.MethodGet, "/api/v1/core/installations", nil, &out)
	return out, err
}
func (c *Client) ImportCore(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/core/import", map[string]string{"path": path}, nil)
}
func (c *Client) UseCore(ctx context.Context, reference string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/core/use", map[string]string{"reference": reference}, nil)
}
func (c *Client) RemoveCore(ctx context.Context, reference string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/core/installations?reference="+url.QueryEscape(reference), nil, nil)
}
func (c *Client) PurgeCores(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/core/installations?all=true", nil, nil)
}
func (c *Client) CoreAction(ctx context.Context, action string) (core.Status, error) {
	var out core.Status
	err := c.do(ctx, http.MethodPost, "/api/v1/core/"+action, map[string]any{}, &out)
	return out, err
}
func (c *Client) SetMode(ctx context.Context, mode string) error {
	settings, err := c.Settings(ctx)
	if err != nil {
		return err
	}
	settings.Mode = mode
	return c.do(ctx, http.MethodPatch, "/api/v1/settings", settings, nil)
}
func (c *Client) Settings(ctx context.Context) (app.Settings, error) {
	var out app.Settings
	err := c.do(ctx, http.MethodGet, "/api/v1/settings", nil, &out)
	return out, err
}
func (c *Client) UpdateSettings(ctx context.Context, settings app.Settings) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/settings", settings, nil)
}
func (c *Client) Logs(ctx context.Context, limit int) ([]string, error) {
	var out []string
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/logs?limit=%d", limit), nil, &out)
	return out, err
}
func (c *Client) Doctor(ctx context.Context) ([]app.DoctorItem, error) {
	var out []app.DoctorItem
	err := c.do(ctx, http.MethodGet, "/api/v1/doctor", nil, &out)
	return out, err
}
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.ShutdownWithResult(ctx)
	return err
}

// ShutdownWithResult 保留恢复警告，不能把“后台已关闭”误解成“所有手动设置都已关闭”。
func (c *Client) ShutdownWithResult(ctx context.Context) (app.ProxyOperationResult, error) {
	var result app.ProxyOperationResult
	err := c.withTimeout(45*time.Second).do(ctx, http.MethodPost, "/api/v1/controller/shutdown", map[string]any{}, &result)
	return result, err
}

func (c *Client) SubscriptionGroups(ctx context.Context) ([]config.SubscriptionGroup, error) {
	var out []config.SubscriptionGroup
	err := c.do(ctx, http.MethodGet, "/api/v1/subscription-groups", nil, &out)
	return out, err
}
func (c *Client) CreateSubscriptionGroup(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/subscription-groups", map[string]string{"name": name}, nil)
}
func (c *Client) RenameSubscriptionGroup(ctx context.Context, name, newName string) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/subscription-groups", map[string]string{"name": name, "newName": newName}, nil)
}
func (c *Client) SetSubscriptionGroup(ctx context.Context, name, action string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/subscription-groups/action", map[string]string{"name": name, "action": action}, nil)
}
func (c *Client) RemoveSubscriptionGroup(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/subscription-groups?name="+url.QueryEscape(name), nil, nil)
}
func (c *Client) Routing(ctx context.Context) (config.RoutingConfig, error) {
	var out config.RoutingConfig
	err := c.do(ctx, http.MethodGet, "/api/v1/routing", nil, &out)
	return out, err
}
func (c *Client) RestoreRouteDefaults(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/v1/routing/restore", map[string]any{}, nil)
}
func (c *Client) UseRouteProfile(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/routing/profiles/use", map[string]string{"name": name}, nil)
}
func (c *Client) CreateRouteProfile(ctx context.Context, profile config.RouteProfile) error {
	return c.do(ctx, http.MethodPost, "/api/v1/routing/profiles", profile, nil)
}
func (c *Client) SetRouteProfileGroup(ctx context.Context, profile, group string, attached bool) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/routing/profiles", map[string]any{"profile": profile, "group": group, "attached": attached}, nil)
}
func (c *Client) DeleteRouteProfile(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/routing/profiles?name="+url.QueryEscape(name), nil, nil)
}
func (c *Client) CreateRuleGroup(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/routing/groups", map[string]string{"name": name}, nil)
}
func (c *Client) DeleteRuleGroup(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/routing/groups?name="+url.QueryEscape(name), nil, nil)
}
func (c *Client) AddRouteRule(ctx context.Context, group string, rule config.RouteRule) error {
	return c.do(ctx, http.MethodPost, "/api/v1/routing/rules", map[string]any{"group": group, "rule": rule}, nil)
}
func (c *Client) UpdateRouteRule(ctx context.Context, group string, index int, expected, rule config.RouteRule) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/routing/rules", map[string]any{"group": group, "index": index, "expected": expected, "rule": rule}, nil)
}
func (c *Client) MoveRouteRule(ctx context.Context, group string, from, to int, expected config.RouteRule) error {
	return c.do(ctx, http.MethodPost, "/api/v1/routing/rules/move", map[string]any{"group": group, "from": from, "to": to, "expected": expected}, nil)
}
func (c *Client) RemoveRouteRuleChecked(ctx context.Context, group string, index int, expected config.RouteRule) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/routing/rules?group=%s&index=%d", url.QueryEscape(group), index), map[string]any{"expected": expected}, nil)
}
func (c *Client) RemoveRouteRule(ctx context.Context, group string, index int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/routing/rules?group=%s&index=%d", url.QueryEscape(group), index), nil, nil)
}
