package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	bridge "github.com/itrunswap/Kivo/internal/desktop"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestTrayConnectionStateUsesLiveEvidence(t *testing.T) {
	now := time.Now()
	base := app.Overview{Core: core.Status{State: "running", CurrentNode: "香港", Mode: "rule"}, SystemProxy: platform.SystemProxyStatus{State: "this_app", Supported: true}, ProxyPortListening: true}
	for _, test := range []struct {
		name   string
		online bool
		mutate func(*app.Overview)
		tone   string
		title  string
	}{
		{"attached", true, func(*app.Overview) {}, "active", "未检测"},
		{"offline", false, func(*app.Overview) {}, "error", "后台未连接"},
		{"dead port", true, func(o *app.Overview) { o.ProxyPortListening = false }, "error", "代理入口异常"},
		{"core only", true, func(o *app.Overview) { o.SystemProxy.State = "off" }, "idle", "仅内核运行"},
		{"unknown", true, func(o *app.Overview) { o.SystemProxy.State = "unknown" }, "error", "未确认"},
		{"fresh", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now, Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}, {ID: "node", State: "ok"}}}
		}, "good", "检测通过"},
		{"old", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now.Add(-3 * time.Minute), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}}}
		}, "active", "未检测"},
		{"future", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now.Add(time.Minute), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}}}
		}, "active", "未检测"},
		{"failed", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now, Routes: []app.ConnectivityRoute{{ID: "entry", State: "failed"}}}
		}, "error", "联网异常"},
		{"direct", true, func(o *app.Overview) { o.Core.Mode = "direct" }, "active", "直连模式"},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := base
			test.mutate(&o)
			v := trayView(o, test.online, false, now)
			if v.Tone != test.tone || !strings.Contains(v.Title, test.title) {
				t.Fatal(v)
			}
		})
	}
}

func TestTrayCannotConnectWithRecoveryOrMissingCore(t *testing.T) {
	o := app.Overview{Core: core.Status{State: "stopped"}, SystemProxy: platform.SystemProxyStatus{State: "off", Supported: true}}
	if !trayView(o, true, false, time.Now()).Connect {
		t.Fatal("normal connect disabled")
	}
	o.SystemProxy.RecoveryPending = true
	if trayView(o, true, false, time.Now()).Connect {
		t.Fatal("recovery ignored")
	}
	o.SystemProxy.RecoveryPending = false
	o.Core.State = "not_installed"
	if trayView(o, true, false, time.Now()).Connect {
		t.Fatal("missing core ignored")
	}
}

func TestNativeMutationsCannotOverlapExitOrOtherMutation(t *testing.T) {
	d := &Desktop{}
	first := d.beginOperation()
	if first == nil {
		t.Fatal("first operation rejected")
	}
	if d.beginOperation() != nil {
		t.Fatal("concurrent mutation accepted")
	}
	if err := d.Quit(false); err == "" {
		t.Fatal("exit accepted during mutation")
	}
	first()
	d.quitting = true
	if d.beginOperation() != nil {
		t.Fatal("mutation accepted during exit")
	}
	d.quitting = false
	d.allowExit = true
	if d.beginOperation() != nil {
		t.Fatal("mutation accepted after exit")
	}
	if d.closing(context.Background()) {
		t.Fatal("explicit exit became hidden")
	}
}

func TestUnavailableTrayNeverHidesWindow(t *testing.T) {
	d := &Desktop{}
	if d.HideWindow() == "" || d.hidden {
		t.Fatal("window lost without tray")
	}
	if d.closing(context.Background()) {
		t.Fatal("normal window cannot close without tray")
	}
}

func TestDisconnectWarningDoesNotRemoveTrayOrApproveExit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/connection/disconnect" {
			t.Error(r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"warning":true,"message":"存在恢复冲突"}}`))
	}))
	defer server.Close()
	paths, err := config.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(c *config.Config) error { c.Web.Listen = strings.TrimPrefix(server.URL, "http://"); return nil }); err != nil {
		t.Fatal(err)
	}
	d := &Desktop{ctx: context.Background(), bridge: bridge.New(paths), trayReady: true}
	if err := d.Quit(true); err != "存在恢复冲突" {
		t.Fatal(err)
	}
	if d.allowExit || d.quitting || !d.trayReady {
		t.Fatal("warning incorrectly permitted shutdown")
	}
	if done := d.beginOperation(); done == nil {
		t.Fatal("exit lock not released")
	} else {
		done()
	}
}
