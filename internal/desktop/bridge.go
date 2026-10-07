// Package desktop 提供不依赖窗口框架的本地控制桥，便于独立测试安全边界。
package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/version"
)

const maxResponse = 16 << 20

// Reply 保留部分成功的数据；批量更新失败不应丢失已更新订阅的节点统计。
type Reply struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
	Status int             `json:"status"`
}

// Info 是可公开的桌面元信息；不返回 Web Token、内核密钥或订阅凭据。
type Info struct {
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Architecture  string `json:"architecture"`
	DataDirectory string `json:"dataDirectory"`
	WebURL        string `json:"webURL"`
}

// Bridge 只允许访问当前数据目录关联的本机 API，不是通用网络代理。
type Bridge struct {
	Paths config.Paths
	http  *http.Client
}

// New 禁用系统代理和重定向，确保本地凭据不会发送到远程地址。
func New(paths config.Paths) *Bridge {
	return &Bridge{Paths: paths, http: &http.Client{
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 20 * time.Minute, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("本地控制接口不允许重定向") },
	}}
}

// LocalURL 把通配监听转换为回环地址，拒绝 DNS 名称和远程监听目标。
func LocalURL(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("后台监听地址无效")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", errors.New("后台监听端口无效")
	}
	switch host {
	case "", "0.0.0.0", "localhost":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("桌面端仅能连接本机回环后台，请将 web.listen 设置为 127.0.0.1、localhost 或通配监听地址")
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

// Info 每次读取最新配置，Web Token 在页面变更后也无需重启桌面程序。
func (b *Bridge) Info() (Info, error) {
	store, err := config.Load(b.Paths)
	if err != nil {
		return Info{}, err
	}
	base, err := LocalURL(store.Snapshot().Web.Listen)
	if err != nil {
		return Info{}, err
	}
	return Info{Version: version.Version, Platform: runtime.GOOS, Architecture: runtime.GOARCH, DataDirectory: b.Paths.Root, WebURL: base}, nil
}

// routes 是明确的业务能力白名单，不能透传内部解密接口或任意路径。
var routes = map[string]string{
	"/overview": "GET", "/nodes": "GET", "/nodes/test": "POST", "/nodes/select": "POST",
	"/connection/connect": "POST", "/connection/disconnect": "POST", "/connectivity/check": "POST",
	"/system-proxy": "GET", "/system-proxy/recover": "POST",
	"/subscriptions": "GET POST PATCH DELETE", "/subscriptions/update": "POST", "/subscriptions/test": "POST",
	"/subscription-groups": "GET POST PATCH DELETE", "/subscription-groups/action": "POST",
	"/routing": "GET", "/routing/restore": "POST", "/routing/profiles": "POST PATCH DELETE", "/routing/profiles/use": "POST",
	"/routing/groups": "POST DELETE", "/routing/rules": "POST PATCH DELETE", "/routing/rules/move": "POST",
	"/settings": "GET PATCH", "/web/security": "GET PATCH", "/logs": "GET", "/doctor": "GET",
	"/core/status": "GET", "/core/start": "POST", "/core/stop": "POST", "/core/restart": "POST",
	"/core/installations": "GET DELETE", "/core/import": "POST", "/core/use": "POST",
}

func validate(method, path, body string) error {
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return errors.New("不允许的控制路径")
	}
	allowed := routes[strings.TrimPrefix(parsed.Path, "/api/v1")]
	if !strings.HasPrefix(parsed.Path, "/api/v1/") || !strings.Contains(" "+allowed+" ", " "+method+" ") || allowed == "" {
		return errors.New("不允许的控制操作")
	}
	// 全量删除内核不暴露给普通节点操作；终止后台仅由原生退出流程控制。
	if parsed.Query().Has("all") {
		return errors.New("桌面端不支持全量清理内核")
	}
	if len(body) > 1<<20 || (body != "" && !json.Valid([]byte(body))) {
		return errors.New("请求 JSON 无效或超过 1 MiB")
	}
	return nil
}

func (b *Bridge) request(ctx context.Context, method, path, body string) (*http.Request, error) {
	store, err := config.Load(b.Paths)
	if err != nil {
		return nil, err
	}
	cfg := store.Snapshot()
	base, err := LocalURL(cfg.Web.Listen)
	if err != nil {
		return nil, err
	}
	if body == "" && method != http.MethodGet {
		body = "{}"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Web.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Web.Secret)
	}
	return req, nil
}

// Request 返回稳定的响应封装，业务错误与传输失败可以分别展示。
func (b *Bridge) Request(ctx context.Context, method, path, body string) Reply {
	if err := validate(method, path, body); err != nil {
		return Reply{Error: err.Error(), Status: 400}
	}
	timeout := 90 * time.Second
	if method == http.MethodGet {
		timeout = 8 * time.Second
	}
	if strings.Contains(path, "/subscriptions/") || path == "/api/v1/nodes/test" {
		timeout = 6 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := b.request(ctx, method, path, body)
	if err != nil {
		return Reply{Error: err.Error()}
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return Reply{Error: "本地后台连接失败，请重新连接后台或查看控制器日志"}
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(content) > maxResponse {
		return Reply{Error: "后台响应读取失败或超过安全大小限制", Status: resp.StatusCode}
	}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return Reply{Error: "后台返回了无效 JSON", Status: resp.StatusCode}
	}
	result := Reply{Data: envelope.Data, Error: envelope.Error.Message, Status: resp.StatusCode}
	if resp.StatusCode >= 400 && result.Error == "" {
		result.Error = fmt.Sprintf("操作失败（HTTP %d）", resp.StatusCode)
	}
	return result
}

// Install 将真实 NDJSON 进度转换为原生事件；只有 complete 才表示安装成功。
func (b *Bridge) Install(ctx context.Context, target string, emit func(core.InstallEvent)) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"version": target})
	req, err := b.request(ctx, "POST", "/api/v1/core/install", string(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := b.http.Do(req)
	if err != nil {
		return errors.New("无法连接内核安装服务")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("内核安装服务返回 HTTP %d，请稍后重试或查看日志", resp.StatusCode)
	}
	reader := bufio.NewScanner(resp.Body)
	reader.Buffer(make([]byte, 4096), 64<<10)
	complete := false
	for reader.Scan() {
		var event core.InstallEvent
		if err := json.Unmarshal(bytes.TrimSpace(reader.Bytes()), &event); err != nil {
			return errors.New("安装进度格式无效")
		}
		if emit != nil {
			emit(event)
		}
		if event.Error != "" {
			return errors.New(event.Error)
		}
		complete = event.Stage == "complete" || complete
	}
	if err := reader.Err(); err != nil {
		return errors.New("安装连接中断；可再次安装以继续下载")
	}
	if !complete {
		return errors.New("安装连接已结束，但后台未确认安装完成")
	}
	return nil
}
