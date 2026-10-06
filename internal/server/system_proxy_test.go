package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

type testSystemProxy struct {
	mu       sync.Mutex
	snapshot platform.ProxySnapshot
	writes   int
}

func (b *testSystemProxy) Capture(context.Context) (platform.ProxySnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return platform.CloneProxySnapshot(b.snapshot), nil
}
func (b *testSystemProxy) Target(before platform.ProxySnapshot, endpoint string) (platform.ProxySnapshot, error) {
	result := platform.CloneProxySnapshot(before)
	result.Entries[0].Values["server"], result.Entries[0].Values["mode"] = endpoint, "on"
	return result, nil
}
func (b *testSystemProxy) Disabled(before platform.ProxySnapshot) platform.ProxySnapshot {
	result := platform.CloneProxySnapshot(before)
	result.Entries[0].Values["mode"] = "off"
	return result
}
func (b *testSystemProxy) Apply(_ context.Context, target platform.ProxySnapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshot = platform.CloneProxySnapshot(target)
	b.writes++
	return nil
}
func (b *testSystemProxy) Lock(context.Context) (func(), error) { return func() {}, nil }
func (b *testSystemProxy) Inspect(snapshot platform.ProxySnapshot, endpoint string) platform.SystemProxyStatus {
	state := "off"
	if snapshot.Entries[0].Values["mode"] == "on" {
		state = "other"
		if snapshot.Entries[0].Values["server"] == endpoint {
			state = "this_app"
		}
	}
	return platform.SystemProxyStatus{State: state, Supported: true, Message: map[string]string{"off": "系统代理已关闭", "this_app": "系统代理指向本程序", "other": "系统代理指向其他程序"}[state]}
}

type proxyFixtureCore struct {
	stubCore
	mu    sync.Mutex
	state core.State
	port  int
}

func (c *proxyFixtureCore) Status(context.Context) core.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return core.Status{Name: "mihomo", State: c.state, Version: "fixture · 非真实内核", MixedPort: c.port, Mode: "rule", EffectiveNode: "隔离测试节点（不连接外网）"}
}
func (c *proxyFixtureCore) Start(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = core.StateRunning
	return nil
}
func (c *proxyFixtureCore) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = core.StateStopped
	return nil
}
func (c *proxyFixtureCore) Restart(context.Context) error { return nil }

func newProxyHTTPFixture(t *testing.T) (*Server, *testSystemProxy, *proxyFixtureCore) {
	t.Helper()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connection.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	if err = store.Update(func(cfg *config.Config) error {
		cfg.Mihomo.MixedPort = port
		cfg.Web.Secret = ""
		cfg.Mihomo.AutoStart = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backend := &testSystemProxy{snapshot: platform.ProxySnapshot{Backend: "fixture", Scope: "test-user", Entries: []platform.ProxyEntry{{Name: "session", Values: map[string]string{"mode": "off", "server": "original-private:80", "pac": "https://private/pac?secret=test-secret"}}}}}
	adapter := &proxyFixtureCore{state: core.StateStopped, port: port}
	return New(app.NewServiceWithSystemProxy(store, adapter, backend), store, nil), backend, adapter
}

func TestSystemProxyHTTPProtectionAndRoundTrip(t *testing.T) {
	s, backend, adapter := newProxyHTTPFixture(t)
	request := func(method, path, body, contentType, origin, site string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:9099"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", site)
		w := httptest.NewRecorder()
		s.handleAPI(w, r)
		return w
	}
	for _, tc := range []struct {
		path, body, mime, origin, site string
		code                           int
	}{
		{"/api/v1/connection/connect", `{}`, "text/plain", "", "", 415},
		{"/api/v1/connection/connect", `{}`, "application/json", "https://evil.example", "", 403},
		{"/api/v1/connection/connect", `{}`, "application/json", "", "cross-site", 403},
		{"/api/v1/core/stop", `{}`, "application/json", "https://evil.example", "", 403},
		{"/api/v1/web/security", `{"token":"evil"}`, "application/json", "https://evil.example", "", 403},
		{"/api/v1/connection/connect", `{"extra":true}`, "application/json", "", "", 400},
		{"/api/v1/system-proxy/off", `{"force":true}`, "application/json", "", "", 400},
		{"/api/v1/connection/disconnect", `{"replace":true}`, "application/json", "", "", 400},
		{"/api/v1/system-proxy/on", `{"skipCheck":true}`, "application/json", "", "", 400},
		{"/api/v1/system-proxy/missing", `{}`, "application/json", "", "", 404},
	} {
		response := request("POST", tc.path, tc.body, tc.mime, tc.origin, tc.site)
		if response.Code != tc.code {
			t.Fatal(tc, response.Code, response.Body.String())
		}
	}
	if backend.writes != 0 || adapter.Status(context.Background()).State != core.StateStopped {
		t.Fatal("invalid request mutated runtime")
	}
	response := request("GET", "/api/v1/system-proxy", "", "", "", "")
	if response.Code != 200 || backend.writes != 0 || strings.Contains(response.Body.String(), "test-secret") || strings.Contains(response.Body.String(), "original-private") {
		t.Fatal("GET leaked private snapshot or mutated state", response.Body.String())
	}
	response = request("POST", "/api/v1/connection/connect", `{"skipCheck":true}`, "application/json", "http://127.0.0.1:9099", "")
	if response.Code != 200 || backend.writes != 1 || !strings.Contains(response.Body.String(), `"managed":true`) {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request("POST", "/api/v1/system-proxy/off", `{}`, "application/json", "", "")
	if response.Code != 200 || adapter.Status(context.Background()).State != core.StateRunning || s.store.Snapshot().SystemProxy.Lease != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request("POST", "/api/v1/system-proxy/on", `{}`, "application/json", "", "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = request("POST", "/api/v1/connection/disconnect", `{}`, "application/json", "", "")
	if response.Code != 200 || adapter.Status(context.Background()).State != core.StateStopped || s.store.Snapshot().SystemProxy.AutoConnect {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestSystemProxyHTTPRequiresConfiguredToken(t *testing.T) {
	s, _, _ := newProxyHTTPFixture(t)
	_ = s.store.Update(func(cfg *config.Config) error { cfg.Web.Secret = "secret"; return nil })
	for _, path := range []string{"/api/v1/system-proxy", "/api/v1/connection/connect", "/api/v1/system-proxy/recover"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleAPI(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code)
		}
	}
}

// 手动启用的浏览器夹具复用真实 Web 服务和业务事务，仅系统写入/内核是内存替身。
// 不接触用户配置、不连订阅或外网；通过真实 shutdown API 结束并检查恢复状态。
func TestBrowserSystemProxyFixture(t *testing.T) {
	listen := os.Getenv("KIVO_BROWSER_FIXTURE")
	if listen == "" {
		t.Skip("manual browser validation fixture")
	}
	s, backend, _ := newProxyHTTPFixture(t)
	original, _ := backend.Capture(context.Background())
	if err := s.store.Update(func(cfg *config.Config) error { cfg.Web.Listen = listen; return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, listen) }()
	select {
	case <-s.Ready():
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	fmt.Printf("BROWSER_FIXTURE http://%s DATA_DIR %s\n", listen, s.store.Paths().Root)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.service.SafeShutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	actual, _ := backend.Capture(context.Background())
	if !platform.EqualProxySnapshots(original, actual) {
		data, _ := json.Marshal(actual)
		t.Fatalf("fixture did not restore: %s", data)
	}
}
