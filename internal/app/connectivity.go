package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
)

const connectivityFreshness = 2 * time.Minute

// ConnectivityProbe 只公开状态和耗时，不公开响应正文、出口 IP 或内部代理密码。
type ConnectivityProbe struct {
	Target     string `json:"target"`
	State      string `json:"state"` // ok / failed / skipped
	DurationMS int64  `json:"durationMs"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Message    string `json:"message"`
}

// ConnectivityRoute 分别描述操作系统路由直连、日常入口、固定节点出口。
type ConnectivityRoute struct {
	ID      string              `json:"id"`
	Label   string              `json:"label"`
	State   string              `json:"state"` // ok / partial / failed / skipped
	Message string              `json:"message"`
	Probes  []ConnectivityProbe `json:"probes"`
}

// ConnectivityReport 是一次手动检测快照。Stale 时禁止用绿色表示“当前可用”。
type ConnectivityReport struct {
	CheckedAt   time.Time           `json:"checkedAt"`
	Stale       bool                `json:"stale"`
	StaleReason string              `json:"staleReason,omitempty"`
	Node        string              `json:"node"`
	Mode        string              `json:"mode"`
	Routes      []ConnectivityRoute `json:"routes"`
}

// Fresh 同时校验标记和时间；CLI 长时间停留时也不能把旧结果当成当前证据。
func (r *ConnectivityReport) Fresh() bool {
	return r != nil && !r.Stale && !r.CheckedAt.IsZero() && time.Since(r.CheckedAt) <= connectivityFreshness
}

// ConnectionSummary 是 CLI/Web 共用的判断结果，避免两端各自猜测“是否启用”。
type ConnectionSummary struct {
	Level       string `json:"level"` // ok / warning / error / idle
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	NextCommand string `json:"nextCommand"`
}

type probeTarget struct {
	name, address string
	status        int
	marker        string
}

func defaultProbeTargets() []probeTarget {
	return []probeTarget{
		{"Google", "https://www.gstatic.com/generate_204", http.StatusNoContent, ""},
		{"Cloudflare", "https://www.cloudflare.com/cdn-cgi/trace", http.StatusOK, "colo="},
	}
}

// CheckConnectivity 仅在用户主动请求时联网。不启动内核、不修改系统代理、不切换模式。
// 固定目标防止远程管理端将该接口用作任意地址扫描器；最多并发六个短请求。
func (s *Service) CheckConnectivity(ctx context.Context) (ConnectivityReport, error) {
	if !s.checkMu.TryLock() {
		return ConnectivityReport{}, errors.New("联网检测正在进行，请稍后查看 /status")
	}
	defer s.checkMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status := s.core.Status(ctx)
	fingerprint := s.connectivityFingerprint(status)
	s.connectivityMu.RLock()
	previous, previousKey := s.lastConnectivity, s.connectivityKey
	s.connectivityMu.RUnlock()
	// 合并连续双击，避免远程客户端反复消耗订阅流量。时间始终保留真实检测时间。
	if previous.Fresh() && previousKey == fingerprint && time.Since(previous.CheckedAt) < 5*time.Second {
		return *previous, nil
	}
	cfg := s.store.Snapshot()
	type routePlan struct {
		id, label, note string
		proxy           *url.URL
		skip            string
	}
	plans := []routePlan{
		{id: "direct", label: "本机直连", note: "不使用应用/环境 HTTP 代理；仍可能受系统 VPN/TUN 路由影响"},
		{id: "entry", label: "日常代理入口", note: "通过本地混合端口，遵循当前模式和规则；成功不代表一定走远端节点", proxy: &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", cfg.Mihomo.MixedPort)}},
		{id: "node", label: "选中节点出口", note: "固定通过 PROXY 策略组，不改变日常路由模式"},
	}
	if status.State != core.StateRunning {
		plans[1].skip, plans[2].skip = "内核未运行，未执行", "内核未运行，未执行"
	} else if provider, ok := s.core.(core.SubscriptionProxy); ok {
		endpointCtx, endpointCancel := context.WithTimeout(ctx, 3*time.Second)
		endpoint, err := provider.SubscriptionProxyURL(endpointCtx)
		endpointCancel()
		if err != nil || endpoint == nil || endpoint.Host == "" {
			plans[2].skip = "无法确认实际代理节点；请检查 PROXY 选择，不能选择 DIRECT/REJECT"
		} else {
			plans[2].proxy = endpoint
		}
	} else {
		plans[2].skip = "当前内核不支持独立节点出口检测"
	}
	if err := ctx.Err(); err != nil {
		return ConnectivityReport{}, err
	}
	report := ConnectivityReport{Mode: status.Mode, Node: defaultString(status.EffectiveNode, status.CurrentNode), Routes: make([]ConnectivityRoute, len(plans))}
	var workers sync.WaitGroup
	for i, plan := range plans {
		report.Routes[i] = ConnectivityRoute{ID: plan.id, Label: plan.label, Message: plan.note, Probes: []ConnectivityProbe{}}
		if plan.skip != "" {
			report.Routes[i].State = "skipped"
			report.Routes[i].Message = plan.skip
			continue
		}
		targets := s.probeTargets
		report.Routes[i].Probes = make([]ConnectivityProbe, len(targets))
		for j, target := range targets {
			workers.Add(1)
			go func(i, j int, target probeTarget, proxy *url.URL) {
				defer workers.Done()
				report.Routes[i].Probes[j] = probeConnectivity(ctx, target, proxy)
			}(i, j, target, plan.proxy)
		}
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return ConnectivityReport{}, err
	}
	for i := range report.Routes {
		if report.Routes[i].State == "skipped" {
			continue
		}
		passed := 0
		for _, probe := range report.Routes[i].Probes {
			if probe.State == "ok" {
				passed++
			}
		}
		report.Routes[i].State = "failed"
		if passed > 0 {
			report.Routes[i].State = "partial"
		}
		if passed == len(report.Routes[i].Probes) && passed > 0 {
			report.Routes[i].State = "ok"
		}
	}
	report.CheckedAt = time.Now()
	if fingerprint != s.connectivityFingerprint(s.core.Status(ctx)) {
		report.Stale = true
		report.StaleReason = "检测期间节点、内核或配置发生变化，请重新检测"
	}
	s.connectivityMu.Lock()
	s.lastConnectivity, s.connectivityKey = &report, fingerprint
	s.connectivityMu.Unlock()
	return report, nil
}

func probeConnectivity(ctx context.Context, target probeTarget, proxy *url.URL) (result ConnectivityProbe) {
	result = ConnectivityProbe{Target: target.name, State: "failed"}
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // 直连检查不能受 HTTP_PROXY/HTTPS_PROXY 影响。
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 8 * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.address, nil)
	if err != nil {
		result.Message = "检测目标配置无效"
		return
	}
	req.Header.Set("User-Agent", "Kivo-Connectivity/1.0")
	response, err := client.Do(req)
	if err != nil {
		result.Message = connectivityError(err)
		return
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	if response.StatusCode != target.status {
		result.Message = fmt.Sprintf("HTTP %d（预期 %d），未确认可达；可能是重定向、访问限制或服务异常", response.StatusCode, target.status)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil {
		result.Message = "读取检测响应失败"
		return
	}
	if len(body) > 8192 || (target.marker != "" && !strings.Contains(string(body), target.marker)) {
		result.Message = "响应内容不符合检测目标预期"
		return
	}
	result.State, result.Message = "ok", "HTTPS 检测通过"
	return
}

func connectivityError(err error) string {
	// 原始 net/http 错误可能包含内部代理密码，绝不原样传出。
	var dns *net.DNSError
	var cert x509.UnknownAuthorityError
	var netErr net.Error
	if errors.As(err, &dns) {
		return "域名解析失败，请检查 DNS 和网络"
	}
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "请求超时，请检查网络或切换节点"
	}
	if errors.As(err, &cert) {
		return "TLS 证书验证失败，不会跳过证书校验"
	}
	return "连接或 TLS 握手失败，请检查端口、网络与节点"
}

func (s *Service) connectivityFingerprint(status core.Status) [32]byte {
	// 不公开指纹内容；包括订阅/路由、进程 PID 和 AUTO 当前实际节点。
	data, _ := json.Marshal(struct {
		Config any
		Status core.Status
	}{s.store.Snapshot(), status})
	return sha256.Sum256(data)
}

func (s *Service) cachedConnectivity(status core.Status) *ConnectivityReport {
	s.connectivityMu.RLock()
	if s.lastConnectivity == nil {
		s.connectivityMu.RUnlock()
		return nil
	}
	report, key := *s.lastConnectivity, s.connectivityKey
	s.connectivityMu.RUnlock()
	if key != s.connectivityFingerprint(status) {
		report.Stale = true
		report.StaleReason = "节点、内核或配置已变化，请重新检测"
	} else if time.Since(report.CheckedAt) > connectivityFreshness {
		report.Stale = true
		report.StaleReason = "结果已超过 2 分钟，请重新检测"
	}
	return &report
}

// SummarizeConnection 仅根据已获得的证据生成状态，不能把进程运行等同于上网成功。
func SummarizeConnection(o Overview) ConnectionSummary {
	if o.SystemProxy.RecoveryPending {
		return ConnectionSummary{"warning", "系统代理有待恢复备份", "原设置备份保留；如果其他程序修改过代理，不会自动覆盖。先安全恢复，再连接。", "/system-proxy recover"}
	}
	if o.Core.State == core.StateNotInstalled {
		return ConnectionSummary{"idle", "代理服务未安装", "请先安装 Mihomo，再添加订阅。", "/install mihomo"}
	}
	if o.Core.State == core.StateFailed {
		return ConnectionSummary{"error", "代理服务异常", "内核未正常运行，请查看日志；系统代理若指向本程序，浏览器可能无法上网。", "/logs 200"}
	}
	if o.Core.State == core.StateStarting || o.Core.State == core.StateStopping {
		return ConnectionSummary{"warning", "代理服务正在切换状态", "请等待操作完成后再检测联网。", "/status"}
	}
	if o.Core.State != core.StateRunning {
		if o.SystemProxy.Supported {
			return ConnectionSummary{"idle", "代理未连接", "连接后自动启动内核并接入系统代理；仅启动内核不会替浏览器设置代理。", "/connect"}
		}
		return ConnectionSummary{"idle", "代理服务未启动", "系统代理若仍指向本程序，浏览器可能无法上网；启动内核或先恢复系统代理设置。", "/core start"}
	}
	if !o.ProxyPortListening {
		return ConnectionSummary{"error", "代理入口未就绪", "内核运行中，但本地端口未监听；尚不能接收浏览器流量。", "/logs 200"}
	}
	if o.SystemProxy.Supported && o.SystemProxy.State != "this_app" {
		return ConnectionSummary{"warning", "内核运行 · 系统未接入", "浏览器尚未通过本程序；使用 /connect 接入并检测。存在其他代理时须先确认覆盖。", "/connect"}
	}
	if !o.Connectivity.Fresh() {
		return ConnectionSummary{"warning", "代理服务已启动 · 外网待检测", "内核运行不等于外网可用；请执行一次联网检测。", "/proxy check"}
	}
	entry, node := "skipped", "skipped"
	for _, r := range o.Connectivity.Routes {
		if r.ID == "entry" {
			entry = r.State
		}
		if r.ID == "node" {
			node = r.State
		}
	}
	if entry == "failed" {
		return ConnectionSummary{"error", "代理入口外网检测未通过", "测试站点无法通过日常入口访问，请查看详细结果；这不代表所有网站均不可达。", "/proxy check"}
	}
	if entry == "partial" || node == "partial" {
		return ConnectionSummary{"warning", "外网部分可达", "部分目标检测失败，建议查看失败原因或切换节点后重试。", "/proxy check"}
	}
	if entry != "ok" {
		return ConnectionSummary{"warning", "尚未确认外网连通性", "请重新检测日常代理入口。", "/proxy check"}
	}
	if strings.EqualFold(o.Core.Mode, "direct") {
		return ConnectionSummary{"warning", "入口可达 · 当前为直连模式", "DIRECT 模式下日常流量不使用远端节点；需要规则分流可切换 RULE。", "/mode rule"}
	}
	if node != "ok" {
		return ConnectionSummary{"warning", "入口可达 · 节点出口未通过", "日常入口能访问测试站点，但不证明远端代理正常；请检查节点选择。", "/node list"}
	}
	if o.SystemProxy.State != "this_app" {
		detail := "测试站点通过代理入口及节点出口可达，但系统 HTTPS 代理未确认接入本程序。"
		if o.TUNEnabled {
			detail += " TUN 仅确认配置开启，尚未验证系统流量实际接管。"
		}
		return ConnectionSummary{"warning", "代理可达 · 系统接入待确认", detail, "/proxy setup"}
	}
	return ConnectionSummary{"ok", "系统已接入 · 外网检测通过", "最近检测中测试站点可达；浏览器扩展、绕过规则和其他网站仍可能表现不同。", "/proxy check"}
}
