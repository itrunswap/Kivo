package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestConnectionSummaryEvidenceTable(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		state                     core.State
		listen                    bool
		entry, node, system, mode string
		stale, tun                bool
		level, title              string
	}{
		{"not installed", core.StateNotInstalled, false, "", "", "off", "rule", false, false, "idle", "未安装"},
		{"stopped with old success", core.StateStopped, true, "ok", "ok", "this_app", "rule", false, false, "idle", "未启动"},
		{"failed", core.StateFailed, false, "", "", "off", "rule", false, false, "error", "异常"},
		{"starting", core.StateStarting, false, "", "", "off", "rule", false, false, "warning", "切换"},
		{"no listener", core.StateRunning, false, "ok", "ok", "this_app", "rule", false, false, "error", "未就绪"},
		{"untested", core.StateRunning, true, "", "", "this_app", "rule", false, false, "warning", "待检测"},
		{"stale green", core.StateRunning, true, "ok", "ok", "this_app", "rule", true, false, "warning", "待检测"},
		{"entry fails", core.StateRunning, true, "failed", "ok", "this_app", "rule", false, false, "error", "未通过"},
		{"partial", core.StateRunning, true, "partial", "ok", "this_app", "rule", false, false, "warning", "部分可达"},
		{"DIRECT", core.StateRunning, true, "ok", "ok", "this_app", "direct", false, false, "warning", "直连模式"},
		{"node fails", core.StateRunning, true, "ok", "failed", "this_app", "rule", false, false, "warning", "节点出口未通过"},
		{"other proxy", core.StateRunning, true, "ok", "ok", "other", "rule", false, false, "warning", "系统接入待确认"},
		{"TUN unverified", core.StateRunning, true, "ok", "ok", "off", "rule", false, true, "warning", "系统接入待确认"},
		{"all proven", core.StateRunning, true, "ok", "ok", "this_app", "rule", false, false, "ok", "检测通过"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := Overview{Core: core.Status{State: tc.state, Mode: tc.mode}, ProxyPortListening: tc.listen, SystemProxy: platform.SystemProxyStatus{State: tc.system}, TUNEnabled: tc.tun}
			if tc.entry != "" {
				o.Connectivity = &ConnectivityReport{CheckedAt: time.Now(), Stale: tc.stale, Routes: []ConnectivityRoute{{ID: "entry", State: tc.entry}, {ID: "node", State: tc.node}}}
			}
			summary := SummarizeConnection(o)
			if summary.Level != tc.level || !strings.Contains(summary.Title, tc.title) || summary.NextCommand == "" {
				t.Fatalf("unexpected summary: %+v", summary)
			}
			if tc.tun && !strings.Contains(summary.Detail, "未验证") {
				t.Fatal(summary.Detail)
			}
		})
	}
}

func TestProbeRejectsRedirectBadContentAndUntrustedTLS(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"pass", 200, "colo=HKG\nip=not-retained", "ok"},
		{"wrong content", 200, "<html>login</html>", "failed"},
		{"redirect", 302, "", "failed"},
		{"server error", 503, "unavailable", "failed"},
		{"oversize", 200, "colo=" + strings.Repeat("x", 8193), "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://example.invalid/forbidden-redirect")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer origin.Close()
			result := probeConnectivity(context.Background(), probeTarget{"fixture", origin.URL, 200, "colo="}, nil)
			if result.State != tc.want {
				t.Fatalf("%+v", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "not-retained") {
				t.Fatal("response body leaked")
			}
		})
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer tlsServer.Close()
	if got := probeConnectivity(context.Background(), probeTarget{"fixture", tlsServer.URL, 204, ""}, nil); got.State != "failed" || !strings.Contains(got.Message, "证书") {
		t.Fatalf("TLS verification bypassed: %+v", got)
	}
}

type checkCore struct {
	fakeCore
	selected string
	endpoint *url.URL
}

func (c *checkCore) Status(context.Context) core.Status {
	return core.Status{State: c.state, Mode: "rule", EffectiveNode: c.selected, PID: 7}
}
func (c *checkCore) SubscriptionProxyURL(context.Context) (*url.URL, error) { return c.endpoint, nil }

func newCheckService(t *testing.T, adapter core.Adapter) *Service {
	t.Helper()
	paths, err := config.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(store, adapter)
}

func TestConnectivityRoutesAreExplicitAndCacheExpires(t *testing.T) {
	var directCalls, entryCalls, nodeCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directCalls.Add(1); w.WriteHeader(204) }))
	defer origin.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entryCalls.Add(1)
		if !r.URL.IsAbs() {
			t.Error("entry did not use explicit proxy")
		}
		w.WriteHeader(204)
	}))
	defer entry.Close()
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodeCalls.Add(1)
		if r.Header.Get("Proxy-Authorization") == "" {
			t.Error("missing private authentication")
		}
		w.WriteHeader(204)
	}))
	defer node.Close()
	proxy, _ := url.Parse(node.URL)
	proxy.User = url.UserPassword("internal", "do-not-leak")
	adapter := &checkCore{fakeCore: fakeCore{state: core.StateRunning}, selected: "node-a", endpoint: proxy}
	s := newCheckService(t, adapter)
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(entry.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	if err := s.store.Update(func(c *config.Config) error { c.Mihomo.MixedPort = port; return nil }); err != nil {
		t.Fatal(err)
	}
	s.probeTargets = []probeTarget{{"fixture", origin.URL, 204, ""}}
	// Direct must bypass environment proxies even when these point to an unavailable endpoint.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	report, err := s.CheckConnectivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range report.Routes {
		if route.State != "ok" {
			t.Fatalf("%+v", route)
		}
	}
	if directCalls.Load() != 1 || entryCalls.Load() != 1 || nodeCalls.Load() != 1 {
		t.Fatalf("wrong paths %d/%d/%d", directCalls.Load(), entryCalls.Load(), nodeCalls.Load())
	}
	if _, err = s.CheckConnectivity(context.Background()); err != nil || directCalls.Load() != 1 {
		t.Fatal("duplicate check not shared", err)
	}
	if s.cachedConnectivity(adapter.Status(context.Background())).Stale {
		t.Fatal("fresh result invalid")
	}
	adapter.selected = "node-b"
	if !s.cachedConnectivity(adapter.Status(context.Background())).Stale {
		t.Fatal("node change retained green")
	}
	adapter.selected = "node-a"
	s.lastConnectivity.CheckedAt = time.Now().Add(-3 * time.Minute)
	if !s.cachedConnectivity(adapter.Status(context.Background())).Stale || s.lastConnectivity.Fresh() {
		t.Fatal("expired result retained green")
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "do-not-leak") || strings.Contains(string(encoded), node.URL) {
		t.Fatal("private endpoint leaked")
	}
}

func TestStoppedCheckDoesNotStartCoreAndCancellationNotCached(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer origin.Close()
	adapter := &fakeCore{state: core.StateStopped}
	s := newCheckService(t, adapter)
	s.probeTargets = []probeTarget{{"fixture", origin.URL, 204, ""}}
	report, err := s.CheckConnectivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Routes[0].State != "ok" || report.Routes[1].State != "skipped" || report.Routes[2].State != "skipped" || adapter.starts != 0 {
		t.Fatalf("unexpected side effects: %+v", report)
	}
	s.lastConnectivity = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.CheckConnectivity(ctx); err == nil || s.lastConnectivity != nil {
		t.Fatal("canceled request saved a result")
	}
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	if _, err = s.CheckConnectivity(context.Background()); err == nil {
		t.Fatal("parallel check should be rejected")
	}
}
