package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

func TestSubscriptionListenerIsPrivateAuthenticatedAndFixed(t *testing.T) {
	path, err := GenerateConfig(config.Config{}, t.TempDir(), RuntimeOptions{SubscriptionPort: 31001, SubscriptionSecret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var generated struct {
		Listeners []struct {
			Listen, Proxy string
			Port          int
			Users         []struct{ Username, Password string }
		}
	}
	if err := json.Unmarshal(data, &generated); err != nil {
		t.Fatal(err)
	}
	if len(generated.Listeners) != 1 {
		t.Fatal("missing private listener")
	}
	listener := generated.Listeners[0]
	if listener.Listen != "127.0.0.1" || listener.Proxy != "PROXY" || listener.Port != 31001 || len(listener.Users) != 1 || listener.Users[0].Password != "test-secret" {
		t.Fatalf("unsafe listener: %+v", listener)
	}
	if _, err := GenerateConfig(config.Config{}, t.TempDir(), RuntimeOptions{SubscriptionPort: 31001}); err == nil {
		t.Fatal("must reject empty listener password")
	}
}

func TestProxySelectionResolvesProviderOnlyNodesAndRejectsDirect(t *testing.T) {
	for _, selection := range []string{"AUTO", "DIRECT", "REJECT", "cycle", "empty"} {
		t.Run(selection, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/providers/proxies":
					json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{"main": providerInfo{Proxies: []proxyInfo{{Name: "provider-only", Type: "Shadowsocks"}}}}})
				case "/proxies/PROXY":
					json.NewEncoder(w).Encode(proxyInfo{Type: "Selector", Now: selection})
				case "/proxies/AUTO":
					json.NewEncoder(w).Encode(proxyInfo{Type: "URLTest", Now: "provider-only"})
				case "/proxies/DIRECT":
					json.NewEncoder(w).Encode(proxyInfo{Type: "Direct"})
				case "/proxies/REJECT":
					json.NewEncoder(w).Encode(proxyInfo{Type: "Reject"})
				case "/proxies/cycle":
					json.NewEncoder(w).Encode(proxyInfo{Type: "Selector", Now: "PROXY"})
				case "/proxies/empty":
					json.NewEncoder(w).Encode(proxyInfo{Type: "URLTest"})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			err := NewClient(strings.TrimPrefix(server.URL, "http://"), "").ValidateProxySelection(context.Background())
			if (err == nil) != (selection == "AUTO") {
				t.Fatalf("selection %s: %v", selection, err)
			}
		})
	}
}

func TestOfflineSnapshotInvalidatesSourceChangesAndClearsHealth(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(c *config.Config) error {
		c.Subscriptions = []config.Subscription{{Name: "main", URL: "https://example.com/private", Enabled: true, Group: "default"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	if err := manager.saveSnapshot("main", core.ProviderSnapshot{Nodes: []core.Node{{Name: "node", Type: "ss", Alive: true, Delay: 30}}}); err != nil {
		t.Fatal(err)
	}
	nodes, err := manager.ListNodes(context.Background())
	if err != nil || len(nodes) != 1 || !nodes[0].Cached || nodes[0].Alive || nodes[0].Delay != 0 {
		t.Fatalf("offline nodes=%+v, %v", nodes, err)
	}
	if err := manager.SelectNode(context.Background(), "node"); err == nil {
		t.Fatal("offline selection must fail")
	}
	store.Update(func(c *config.Config) error { c.Subscriptions[0].UpdateVia = "proxy"; return nil })
	if len(manager.cachedNodes()) != 1 {
		t.Fatal("route change invalidated source")
	}
	store.Update(func(c *config.Config) error { c.Subscriptions[0].URL = "https://example.com/different"; return nil })
	if len(manager.cachedNodes()) != 0 {
		t.Fatal("source change retained stale nodes")
	}
}

func TestProviderOldNodesCannotMasqueradeAsFreshUpdate(t *testing.T) {
	updated := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{"main": providerInfo{UpdatedAt: updated.Format(time.RFC3339), Proxies: []proxyInfo{{Name: "old", Type: "ss"}}}}})
	}))
	defer server.Close()
	_, err := waitProviderNodes(context.Background(), NewClient(strings.TrimPrefix(server.URL, "http://"), ""), "main", 25*time.Millisecond, updated)
	if err == nil || !strings.Contains(err.Error(), "更新时间未变化") {
		t.Fatalf("old content must not report success: %v", err)
	}
}

func TestSubscriptionErrorsRedactURLPathsAndRepeatedTokens(t *testing.T) {
	value := sanitizeLog(`Get "https://example.com/private-path-secret?token=one": failed; token=two password=three token=four`)
	for _, secret := range []string{"private-path-secret", "one", "two", "three", "four"} {
		if strings.Contains(value, secret) {
			t.Fatalf("leaked credential %q", secret)
		}
	}
}
