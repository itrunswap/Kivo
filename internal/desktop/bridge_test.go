package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

func testBridge(t *testing.T, handler http.HandlerFunc) (*Bridge, *config.Store) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	paths, err := config.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(c *config.Config) error {
		c.Web.Listen = strings.TrimPrefix(srv.URL, "http://")
		c.Web.Secret = "test-token"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(paths), store
}

func TestLocalURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"0.0.0.0:1234", "http://127.0.0.1:1234"}, {"localhost:1234", "http://127.0.0.1:1234"}, {"[::]:1234", "http://[::1]:1234"}, {"127.0.0.1:1234", "http://127.0.0.1:1234"}} {
		got, err := LocalURL(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"example.com:80", "192.168.1.2:1234", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:http", "http://127.0.0.1:1234"} {
		if _, err := LocalURL(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestRequestAllowlist(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{{"GET", "https://example.com/api/v1/overview", ""}, {"GET", "//example.com/api/v1/overview", ""}, {"GET", "/api/v1/internal/subscription-content", ""}, {"POST", "/api/v1/controller/shutdown", "{}"}, {"GET", "/api/v1/settings/../overview", ""}, {"POST", "/api/v1/core/unknown", "{}"}, {"GET", "/api/v1/%6fverview", ""}, {"DELETE", "/api/v1/core/installations?all=true", "{}"}, {"PATCH", "/api/v1/settings", "invalid"}, {"GET", "/api/v1/session/verify", ""}} {
		if err := validate(tc.method, tc.path, tc.body); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/api/v1/overview"}, {"PATCH", "/api/v1/settings"}, {"POST", "/api/v1/subscriptions/update"}, {"DELETE", "/api/v1/subscriptions?name=test"}} {
		if err := validate(tc.method, tc.path, "{}"); err != nil {
			t.Fatal(err)
		}
	}
}

// 页面新增操作必须同时通过桌面桥和后台 API；只测 JS 的模拟请求会漏掉白名单漂移。
func TestDesktopManagementRoutesReachBackend(t *testing.T) {
	tests := []struct {
		name, method, path, body string
	}{
		{"rename subscription group", "PATCH", "/api/v1/subscription-groups", `{"name":"old","newName":"new"}`},
		{"restore routing presets", "POST", "/api/v1/routing/restore", `{}`},
		{"edit route rule", "PATCH", "/api/v1/routing/rules", `{"group":"custom","index":1,"rule":{"type":"domain","value":"example.com","action":"proxy"}}`},
		{"move route rule", "POST", "/api/v1/routing/rules/move", `{"group":"custom","from":1,"to":2}`},
		{"delete checked rule", "DELETE", "/api/v1/routing/rules?group=custom&index=1", `{"expected":{"type":"domain","value":"example.com","action":"proxy"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			bridge, _ := testBridge(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != test.method || r.URL.RequestURI() != test.path {
					t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.RequestURI())
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("missing local auth: %q", got)
				}
				var body json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !json.Valid(body) {
					t.Errorf("invalid JSON body: %v", err)
				}
				_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
			})
			result := bridge.Request(context.Background(), test.method, test.path, test.body)
			if result.Error != "" || result.Status != http.StatusOK || calls.Load() != 1 {
				t.Fatalf("desktop action did not reach backend: %+v, calls=%d", result, calls.Load())
			}
		})
	}
	for _, rejected := range []struct{ method, path string }{
		{"GET", "/api/v1/routing/restore"},
		{"DELETE", "/api/v1/routing/rules/move"},
		{"POST", "/api/v1/subscription-groups/unknown"},
	} {
		if err := validate(rejected.method, rejected.path, `{}`); err == nil {
			t.Fatalf("unexpected desktop capability: %s %s", rejected.method, rejected.path)
		}
	}
}

func TestTokenRotationAndPublicInfo(t *testing.T) {
	expected := "test-token"
	b, store := testBridge(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expected {
			t.Errorf("wrong token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	})
	if got := b.Request(context.Background(), "GET", "/api/v1/overview", ""); got.Error != "" || got.Status != 200 {
		t.Fatalf("%+v", got)
	}
	expected = "rotated-token"
	if err := store.Update(func(c *config.Config) error { c.Web.Secret = expected; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := b.Request(context.Background(), "GET", "/api/v1/overview", ""); got.Error != "" {
		t.Fatal(got.Error)
	}
	info, err := b.Info()
	if err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(info)
	if strings.Contains(string(content), expected) || strings.Contains(string(content), "secret") {
		t.Fatal("credential exposed")
	}
}

func TestPartialFailurePreservesData(t *testing.T) {
	b, _ := testBridge(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"data":{"results":[{"name":"one","nodeCount":3}]},"error":{"message":"second failed"}}`))
	})
	got := b.Request(context.Background(), "POST", "/api/v1/subscriptions/update", "{}")
	if got.Status != 502 || got.Error != "second failed" || !strings.Contains(string(got.Data), "nodeCount") {
		t.Fatalf("%+v", got)
	}
}

func TestRedirectDoesNotSendCredentials(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	b, _ := testBridge(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) })
	if got := b.Request(context.Background(), "GET", "/api/v1/overview", ""); got.Error == "" {
		t.Fatal("accepted redirect")
	}
	if called {
		t.Fatal("followed redirect")
	}
}

func TestInstallRequiresTerminalMarker(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{{`{"stage":"download","downloaded":10,"total":20}` + "\n", false}, {`{"stage":"complete","message":"installed"}` + "\n", true}, {`{"stage":"error","error":"failed"}` + "\n", false}, {"bad\n", false}} {
		b, _ := testBridge(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/x-ndjson" {
				t.Error("not streaming")
			}
			_, _ = w.Write([]byte(tc.body))
		})
		count := 0
		err := b.Install(context.Background(), "", func(core.InstallEvent) { count++ })
		if (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if tc.ok && count != 1 {
			t.Fatal(count)
		}
	}
}

func TestInvalidResponseAndCancelledContext(t *testing.T) {
	b, _ := testBridge(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) })
	if got := b.Request(context.Background(), "GET", "/api/v1/overview", ""); got.Error == "" {
		t.Fatal("accepted invalid response")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := b.Request(ctx, "GET", "/api/v1/overview", ""); got.Error == "" {
		t.Fatal("ignored cancellation")
	}
}
