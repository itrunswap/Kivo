package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

// TestBuiltCLIStatusCard 启动真实构建产物，验证命令入口和状态卡集成。
// 控制接口使用回环测试夹具，配置使用临时目录；不会接触用户订阅或系统代理。
// 构建完成后设置 KIVO_TEST_BINARY 为绝对路径，即可运行此测试。
func TestBuiltCLIStatusCard(t *testing.T) {
	binary := os.Getenv("KIVO_TEST_BINARY")
	if binary == "" {
		t.Skip("requires an explicitly selected built CLI")
	}
	for _, state := range []string{"ready", "pending", "off", "failed", "stopped"} {
		t.Run(state, func(t *testing.T) {
			o := app.Overview{
				Core:               core.Status{State: core.StateRunning, MixedPort: 17890, Mode: "rule", EffectiveNode: "测试节点", Version: "v1.19.31"},
				ProxyPortListening: true, SystemProxy: platform.SystemProxyStatus{State: "this_app"}, EnabledCount: 1, SubscriptionCount: 2,
				Connectivity: &app.ConnectivityReport{CheckedAt: time.Now(), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}, {ID: "node", State: "ok"}}},
			}
			want := "代理已启用 · 外网检测通过"
			switch state {
			case "pending":
				o.Connectivity, want = nil, "外网待检测"
			case "off":
				o.SystemProxy.State, want = "off", "系统接入待确认"
			case "failed":
				o.Connectivity.Routes[1].State, want = "failed", "节点出口未通过"
			case "stopped":
				o.Core.State, o.ProxyPortListening, want = core.StateStopped, false, "系统仍指向本程序"
			}
			var unexpected atomic.Int32
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/health" && r.Method == http.MethodGet {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.Header.Get("Authorization") != "Bearer panel-test-only" {
					unexpected.Add(1)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.URL.Path == "/api/v1/session/verify" && r.Method == http.MethodPost {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == "/api/v1/overview" && r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(map[string]any{"data": o}); err != nil {
						t.Error(err)
					}
					return
				}
				unexpected.Add(1)
				http.NotFound(w, r)
			}))
			defer controller.Close()
			paths, err := config.ResolvePaths(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store, err := config.Load(paths)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Update(func(cfg *config.Config) error {
				cfg.Web.Listen, cfg.Web.Secret = strings.TrimPrefix(controller.URL, "http://"), "panel-test-only"
				cfg.Mihomo.AutoStart = false
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"status"}, {"st"}, {"proxy"}, {"proxy", "status"}, {}} {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				command := exec.CommandContext(ctx, binary, append([]string{"--data-dir", paths.Root}, args...)...)
				command.Env = append(os.Environ(), "NO_COLOR=1")
				command.Stdin = strings.NewReader("/status\n/quit\n")
				output, err := command.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("%v: %v\n%s", args, err, output)
				}
				text := string(output)
				for _, required := range []string{"连接概览", want, "服务", "系统", "联网", "下一步", "127.0.0.1:17890"} {
					if !strings.Contains(text, required) {
						t.Errorf("%v missing %q:\n%s", args, required, text)
					}
				}
				if strings.Contains(text, "\x1b") {
					t.Errorf("%v ignored NO_COLOR", args)
				}
			}
			if unexpected.Load() != 0 {
				t.Fatalf("status/quit issued %d unexpected requests", unexpected.Load())
			}
		})
	}
}
