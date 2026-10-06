package mihomo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListNodesUsesProviderCatalogWhenProxyGroupHasNoExpandedNodes(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxies/PROXY":
			_, _ = w.Write([]byte(`{"name":"PROXY","type":"Selector","now":"DIRECT","all":["AUTO","DIRECT"]}`))
		case "/proxies":
			_, _ = w.Write([]byte(`{"proxies":{"Hong Kong 01":{"name":"Hong Kong 01","type":"Shadowsocks","alive":true,"udp":true,"history":[{"delay":86}]}}}`))
		case "/providers/proxies":
			_, _ = w.Write([]byte(`{"providers":{"mojie":{"name":"mojie","vehicleType":"HTTP","proxies":[{"name":"Hong Kong 01","type":"Shadowsocks","alive":true,"udp":true}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(strings.TrimPrefix(server.URL, "http://"), "secret")
	nodes, current, err := client.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current != "DIRECT" || len(nodes) != 1 {
		t.Fatalf("current=%q nodes=%#v", current, nodes)
	}
	if nodes[0].Name != "Hong Kong 01" || nodes[0].ProviderName != "mojie" || nodes[0].Delay != 86 || !nodes[0].Tested {
		t.Fatalf("unexpected node: %#v", nodes[0])
	}
	count, err := client.ProviderNodeCount(context.Background(), "MOJIE")
	if err != nil || count != 1 {
		t.Fatalf("provider node count=%d error=%v", count, err)
	}
}

func TestNodeTestsIncludeFailuresAndExcludeStrategies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxies/PROXY":
			w.Write([]byte(`{"now":"AUTO","all":["AUTO","DIRECT","B","A"]}`))
		case "/proxies":
			w.Write([]byte(`{"proxies":{"A":{"name":"A","type":"Shadowsocks","alive":true},"B":{"name":"B","type":"VLESS","alive":true,"history":[{"delay":1}]}}}`))
		case "/providers/proxies":
			w.Write([]byte(`{"providers":{"test":{"proxies":[{"name":"B","type":"VLESS"},{"name":"A","type":"Shadowsocks"}]}}}`))
		case "/group/PROXY/delay":
			w.Write([]byte(`{"DIRECT":1,"AUTO":2,"A":88}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(strings.TrimPrefix(server.URL, "http://"), "secret")
	nodes, _, err := client.ListNodes(context.Background())
	if err != nil || len(nodes) != 2 || nodes[0].Name != "A" || nodes[0].Tested || !nodes[1].Tested {
		t.Fatalf("stable nodes/history: %+v %v", nodes, err)
	}
	result, err := client.TestNodes(context.Background())
	if err != nil || len(result) != 2 || result["A"] != 88 || result["B"] != 0 {
		t.Fatalf("normalized tests: %+v %v", result, err)
	}
}

func TestWaitProviderNodesHandlesAsynchronousRefresh(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) < 2 {
			_, _ = w.Write([]byte(`{"providers":{"primary":{"name":"primary","proxies":[]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"providers":{"primary":{"name":"primary","proxies":[{"name":"node","type":"VLESS"}]}}}`))
	}))
	defer server.Close()

	client := NewClient(strings.TrimPrefix(server.URL, "http://"), "secret")
	count, err := waitProviderNodes(context.Background(), client, "primary", time.Second)
	if err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
}

func TestProviderNodeCountRejectsMissingProvider(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"providers":{}}`))
	}))
	defer server.Close()

	client := NewClient(strings.TrimPrefix(server.URL, "http://"), "secret")
	if _, err := client.ProviderNodeCount(context.Background(), "missing"); err == nil {
		t.Fatal("missing provider should return an error")
	}
}

func TestUpdateProviderUsesLongOperationTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(strings.TrimPrefix(server.URL, "http://"), "secret")
	client.http.Timeout = 5 * time.Millisecond
	if err := client.UpdateProvider(context.Background(), "primary"); err != nil {
		t.Fatalf("provider update should not inherit the short query timeout: %v", err)
	}
}
