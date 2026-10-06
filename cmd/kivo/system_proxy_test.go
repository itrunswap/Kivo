package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
)

// 所有新增命令启动真实发行 exe，控制服务仍使用回环夹具，避免写真实系统设置。
func TestBuiltCLIConnectionCommands(t *testing.T) {
	binary := os.Getenv("KIVO_TEST_BINARY")
	if binary == "" {
		t.Skip("requires built CLI")
	}
	var paths []string
	var bodies []map[string]bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/session/verify" {
			fmtTestEnvelope(t, w, map[string]bool{"valid": true})
			return
		}
		if r.Header.Get("Authorization") != "Bearer cli-test" {
			t.Error("missing authentication")
			w.WriteHeader(401)
			return
		}
		paths = append(paths, r.URL.Path)
		if r.Method == "GET" {
			fmtTestEnvelope(t, w, map[string]any{"state": "this_app", "message": "自动接入", "managed": true, "supported": true})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON MIME")
		}
		var body map[string]bool
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		fmtTestEnvelope(t, w, map[string]string{"message": "fixture 操作成功"})
	}))
	defer controller.Close()
	pathsConfig, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(pathsConfig)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(cfg *config.Config) error {
		cfg.Web.Listen = strings.TrimPrefix(controller.URL, "http://")
		cfg.Web.Secret = "cli-test"
		return nil
	})
	for _, args := range [][]string{{"connect", "--no-check"}, {"connect", "--replace", "--no-check"}, {"disconnect"}, {"system-proxy", "status"}, {"system-proxy", "on", "--adopt"}, {"system-proxy", "off"}, {"system-proxy", "recover", "--force"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, binary, append([]string{"--data-dir", pathsConfig.Root}, args...)...)
		command.Env = append(os.Environ(), "NO_COLOR=1")
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatal(args, err, string(output))
		}
		isStatus := len(args) > 1 && args[0] == "system-proxy" && args[1] == "status"
		if !strings.Contains(string(output), "fixture 操作成功") && !isStatus {
			t.Fatal(args, string(output))
		}
	}
	if len(paths) != 7 || len(bodies) != 6 || !bodies[1]["replace"] || !bodies[1]["skipCheck"] || !bodies[3]["adopt"] || !bodies[5]["force"] {
		t.Fatal(paths, bodies)
	}
}

func fmtTestEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		t.Error(err)
	}
}

func TestBuiltCLIOfflineProxyCommandsDoNotSpawnDaemon(t *testing.T) {
	binary := os.Getenv("KIVO_TEST_BINARY")
	if binary == "" {
		t.Skip("requires built CLI")
	}
	// 明确不可用的测试 HTTP 服务模拟后台停止；任何意外自动启动都能从日志检测。
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer controller.Close()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(cfg *config.Config) error {
		cfg.Web.Listen = strings.TrimPrefix(controller.URL, "http://")
		cfg.Mihomo.AutoStart = true
		return nil
	})
	for _, args := range [][]string{{"system-proxy", "status"}, {"system-proxy", "off"}, {"system-proxy", "recover"}, {"disconnect"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, binary, append([]string{"--data-dir", paths.Root}, args...)...)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatal(args, err, string(output))
		}
		if _, err := os.Stat(filepath.Join(paths.LogDir, "controller.log")); !os.IsNotExist(err) {
			t.Fatal("offline command started daemon", args, err)
		}
	}
}
