package mihomo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
)

// TestRealCoreSubscriptionRoutes 仅在显式指定测试内核时运行。订阅服务器、代理
// 节点和所有端口都在回环地址，不访问真实订阅、不更改系统代理或用户配置。
func TestRealCoreSubscriptionRoutes(t *testing.T) {
	binary := os.Getenv("KIVO_TEST_MIHOMO")
	if binary == "" {
		t.Skip("设置 KIVO_TEST_MIHOMO 以运行真实内核回环集成测试")
	}
	var viaProxy atomic.Int32
	var payload []byte
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer origin.Close()
	originAddress := strings.TrimPrefix(origin.URL, "http://")
	upstreamProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mihomo 的 HTTP 出站对 HTTP 目标也使用 CONNECT。只允许隧道连接
		// 本测试的源站，拒绝健康检查等其他目的地，确保测试不访问公网。
		if r.Method != http.MethodConnect || r.Host != originAddress {
			http.Error(w, "health-check fixture", 502)
			return
		}
		destination, err := net.DialTimeout("tcp", originAddress, time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", 502)
			return
		}
		defer destination.Close()
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		fmt.Fprint(buffered, "HTTP/1.1 200 Connection established\r\n\r\n")
		buffered.Flush()
		viaProxy.Add(1)
		go func() { io.Copy(destination, buffered); destination.Close() }()
		io.Copy(connection, destination)
	}))
	defer upstreamProxy.Close()
	_, proxyPort, _ := net.SplitHostPort(strings.TrimPrefix(upstreamProxy.URL, "http://"))
	payload = []byte(fmt.Sprintf("proxies:\n  - name: local-test-node\n    type: http\n    server: 127.0.0.1\n    port: %s\n", proxyPort))
	freePort := func() int {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		return listener.Addr().(*net.TCPAddr).Port
	}
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Mihomo.BinaryPath = binary
		c.Mihomo.AutoStart = false
		c.Mihomo.TUNEnabled = false
		c.Mihomo.Mode = "direct"
		c.Mihomo.Controller = "127.0.0.1:" + strconv.Itoa(freePort())
		c.Mihomo.MixedPort = freePort()
		c.Subscriptions = []config.Subscription{{Name: "fixture", URL: origin.URL + "/subscription", Enabled: true, Group: "default", UpdateVia: "direct"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	defer func() {
		if err := manager.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := manager.UpdateSubscription(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	pid := manager.Status(ctx).PID
	if err := manager.SelectNode(ctx, "local-test-node"); err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) error { c.Subscriptions[0].UpdateVia = "proxy"; return nil })
	if err := manager.Reload(ctx); err != nil {
		t.Fatalf("hot reload: %v", err)
	}
	if manager.Status(ctx).PID != pid {
		t.Fatal("hot reload restarted process")
	}
	before := viaProxy.Load()
	if err := manager.UpdateSubscription(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	if viaProxy.Load() <= before {
		t.Fatal("ordinary provider proxy update did not use proxy in DIRECT mode")
	}
	// AES 适配器使用同一固定出站入口，验证其不受 DIRECT 模式影响。
	endpoint, err := manager.SubscriptionProxyURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(endpoint)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	before = viaProxy.Load()
	response, err := client.Get(origin.URL + "/subscription")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || viaProxy.Load() <= before {
		t.Fatalf("fixed listener bypassed proxy: status=%d", response.StatusCode)
	}
	// 未携带内部密码不得借用该固定出口。
	endpoint.User = nil
	unauthorizedTransport := &http.Transport{Proxy: http.ProxyURL(endpoint)}
	defer unauthorizedTransport.CloseIdleConnections()
	unauthorized := &http.Client{Transport: unauthorizedTransport, Timeout: 5 * time.Second}
	response, err = unauthorized.Get(origin.URL + "/subscription")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("internal proxy accepts unauthenticated requests: %d", response.StatusCode)
	}
	store.Update(func(c *config.Config) error { c.Subscriptions[0].UpdateVia = "direct"; return nil })
	if err := manager.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	before = viaProxy.Load()
	if err := manager.UpdateSubscription(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	if viaProxy.Load() != before {
		t.Fatal("direct update used proxy")
	}
	if manager.Status(ctx).PID != pid {
		t.Fatal("route switching restarted core")
	}
	// 真实混合端口的 GLOBAL 必须沿用 PROXY，不能只验证 mode 字段。
	if err := manager.SetMode(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	mixed, _ := neturl.Parse(fmt.Sprintf("http://127.0.0.1:%d", store.Snapshot().Mihomo.MixedPort))
	mixedTransport := &http.Transport{Proxy: http.ProxyURL(mixed)}
	defer mixedTransport.CloseIdleConnections()
	mixedClient := &http.Client{Transport: mixedTransport, Timeout: 5 * time.Second}
	before = viaProxy.Load()
	response, err = mixedClient.Get(origin.URL + "/global-proof")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || viaProxy.Load() <= before {
		t.Fatal("GLOBAL entry bypassed selected proxy")
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// 删除测试缓存，证明恢复依赖应用持久化而非 Mihomo 恰好记住上次选择。
	if err := os.Remove(filepath.Join(paths.RuntimeDir, "mihomo", "cache.db")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := manager.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if manager.Status(ctx).PID == pid {
		t.Fatal("explicit restart did not replace core")
	}
	status := manager.Status(ctx)
	if status.CurrentNode != "local-test-node" || status.Mode != "global" {
		t.Fatalf("restored status: %+v", status)
	}
	before = viaProxy.Load()
	response, err = mixedClient.Get(origin.URL + "/restart-proof")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if viaProxy.Load() <= before {
		t.Fatal("GLOBAL restart bypassed proxy")
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	nodes, err := manager.ListNodes(ctx)
	if err != nil || len(nodes) != 1 || !nodes[0].Cached {
		t.Fatalf("offline snapshot: %+v %v", nodes, err)
	}
}
