package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/itrunswap/Kivo/internal/client"
)

// TestShutdownStopsCoreAndExitsInteractiveShell 验证完整退出的关键顺序：
// 先停止 Core，再关闭控制服务，且成功后不再等待下一条终端输入。
func TestShutdownStopsCoreAndExitsInteractiveShell(t *testing.T) {
	t.Parallel()

	var running atomic.Bool
	running.Store(true)
	var coreStopped atomic.Bool
	var controllerStopped atomic.Bool

	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/health":
			if !running.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeTestEnvelope(t, w, map[string]string{"status": "ok"})
		case "/api/v1/session/verify":
			writeTestEnvelope(t, w, map[string]bool{"valid": true})
		case "/api/v1/overview":
			writeTestEnvelope(t, w, map[string]any{
				"core": map[string]any{"name": "mihomo", "state": "running", "mixedPort": 17890, "mode": "rule"},
			})
		case "/api/v1/core/stop":
			coreStopped.Store(true)
			writeTestEnvelope(t, w, map[string]any{"name": "mihomo", "state": "stopped"})
		case "/api/v1/controller/shutdown":
			if !coreStopped.Load() {
				t.Error("controller was stopped before core")
			}
			controllerStopped.Store(true)
			writeTestEnvelope(t, w, map[string]string{"status": "shutting_down"})
			running.Store(false)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	address := strings.TrimPrefix(controller.URL, "http://")
	var output bytes.Buffer
	shell := NewShell(client.New(address, "test-token"), strings.NewReader("/shutdown\n"), &output, controller.URL, "test-token")
	if err := shell.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !coreStopped.Load() || !controllerStopped.Load() {
		t.Fatalf("shutdown state: core=%t controller=%t", coreStopped.Load(), controllerStopped.Load())
	}
	if !strings.Contains(output.String(), "Kivo 已完全退出") {
		t.Fatalf("output should confirm complete shutdown, got %q", output.String())
	}
}

func TestParseSubscriptionUpdateArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		reference string
		group     string
		via       string
	}{
		{name: "empty means all", reference: "all"},
		{name: "single by index", args: []string{"2"}, reference: "2"},
		{name: "group", args: []string{"group", "工作"}, reference: "all", group: "工作"},
		{name: "group option", args: []string{"--group", "工作", "--via", "proxy"}, reference: "all", group: "工作", via: "proxy"},
		{name: "direct alias", args: []string{"all", "--direct"}, reference: "all", via: "direct"},
		{name: "proxy alias", args: []string{"主线路", "--proxy"}, reference: "主线路", via: "proxy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseSubscriptionUpdateArgs(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if got.Reference != test.reference || got.Group != test.group || got.Via != test.via {
				t.Fatalf("parse result = %#v", got)
			}
		})
	}
	if _, err := parseSubscriptionUpdateArgs([]string{"all", "--proxy", "--via", "direct"}); err == nil {
		t.Fatal("conflicting update routes should fail")
	}
}

func TestShutdownClearsCoreAutoStartWhenControllerIsStopped(t *testing.T) {
	t.Parallel()

	var running atomic.Bool
	var offlineShutdown atomic.Bool
	var coreStopped atomic.Bool
	var controllerStopped atomic.Bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/health":
			if !running.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeTestEnvelope(t, w, map[string]string{"status": "ok"})
		case "/api/v1/session/verify":
			writeTestEnvelope(t, w, map[string]bool{"valid": true})
		case "/api/v1/core/stop":
			coreStopped.Store(true)
			writeTestEnvelope(t, w, map[string]any{"name": "mihomo", "state": "stopped"})
		case "/api/v1/controller/shutdown":
			controllerStopped.Store(true)
			writeTestEnvelope(t, w, map[string]string{"status": "shutting_down"})
			running.Store(false)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	address := strings.TrimPrefix(controller.URL, "http://")
	var output bytes.Buffer
	shell := NewShell(client.New(address, "test-token"), strings.NewReader(""), &output, controller.URL, "test-token")
	shell.SetOfflineShutdown(func() error {
		offlineShutdown.Store(true)
		return nil
	})
	if err := shell.Execute(context.Background(), []string{"shutdown"}); err != nil {
		t.Fatalf("shutdown error = %v", err)
	}
	if !offlineShutdown.Load() || coreStopped.Load() || controllerStopped.Load() {
		t.Fatalf("shutdown state: offline=%t core=%t controller=%t", offlineShutdown.Load(), coreStopped.Load(), controllerStopped.Load())
	}
}

func writeTestEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
