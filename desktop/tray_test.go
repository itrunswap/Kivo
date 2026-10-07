package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	bridge "github.com/itrunswap/Kivo/internal/desktop"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestTrayTUNAvailabilityUsesCurrentCoreState(t *testing.T) {
	o := app.Overview{Core: core.Status{State: "running"}, TUNEnabled: false}
	if !trayView(o, true, false, time.Now()).TUNCanChange {
		t.Fatal("running core should permit TUN enable")
	}
	o.Core.State = "stopped"
	if trayView(o, true, false, time.Now()).TUNCanChange {
		t.Fatal("stopped core should not offer TUN enable")
	}
	o.TUNEnabled = true
	if !trayView(o, true, false, time.Now()).TUNCanChange {
		t.Fatal("configured TUN must remain disableable")
	}
	if trayView(o, false, false, time.Now()).TUNCanChange {
		t.Fatal("unknown backend must not change TUN")
	}
}

func TestTrayTUNWritesOnlyFreshTUNField(t *testing.T) {
	current := false
	var coreState atomic.Value
	coreState.Store("running")
	var patches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/settings":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"revision": "fresh-revision", "tunEnabled": current}})
		case "GET /api/v1/overview":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"core": map[string]any{"state": coreState.Load()}}})
		case "PATCH /api/v1/settings":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 2 || body["revision"] != "fresh-revision" || body["tunEnabled"] != true {
				t.Errorf("unsafe settings patch: %#v", body)
			}
			patches.Add(1)
			current = true
			_, _ = w.Write([]byte(`{"data":{"tunEnabled":true}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
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
	if err := store.Update(func(c *config.Config) error { c.Web.Listen = strings.TrimPrefix(server.URL, "http://"); return nil }); err != nil {
		t.Fatal(err)
	}
	d := &Desktop{ctx: context.Background(), bridge: bridge.New(paths)}
	coreState.Store("stopped")
	if err := d.setTrayTUN(true); err == nil || patches.Load() != 0 {
		t.Fatal("enabled TUN without running core", err)
	}
	coreState.Store("running")
	if err := d.setTrayTUN(true); err != nil || patches.Load() != 1 {
		t.Fatal("TUN patch failed", err, patches.Load())
	}
	if err := d.setTrayTUN(true); err != nil || patches.Load() != 1 {
		t.Fatal("stale menu click reversed or repeated TUN", err, patches.Load())
	}
}

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
		{"attached", true, func(*app.Overview) {}, "active", "已启用"},
		{"offline", false, func(*app.Overview) {}, "error", "状态暂不可用"},
		{"dead port", true, func(o *app.Overview) { o.ProxyPortListening = false }, "error", "未启用"},
		{"core only", true, func(o *app.Overview) { o.SystemProxy.State = "off" }, "idle", "未启用"},
		{"unknown", true, func(o *app.Overview) { o.SystemProxy.State = "unknown" }, "idle", "未启用"},
		{"fresh", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now, Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}, {ID: "node", State: "ok"}}}
		}, "active", "已启用"},
		{"old", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now.Add(-3 * time.Minute), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}}}
		}, "active", "已启用"},
		{"future", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now.Add(time.Minute), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}}}
		}, "active", "已启用"},
		{"failed", true, func(o *app.Overview) {
			o.Connectivity = &app.ConnectivityReport{CheckedAt: now, Routes: []app.ConnectivityRoute{{ID: "entry", State: "failed"}}}
		}, "active", "已启用"},
		{"direct", true, func(o *app.Overview) { o.Core.Mode = "direct" }, "active", "已启用"},
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

func TestTrayConfirmHandlesWindowsAndNativeButtonResults(t *testing.T) {
	for _, answer := range []string{"确认", "Yes"} {
		if !confirmedTrayAnswer(answer) {
			t.Fatalf("confirmed answer rejected: %q", answer)
		}
	}
	for _, answer := range []string{"取消", "No", "Error", ""} {
		if confirmedTrayAnswer(answer) {
			t.Fatalf("cancel answer accepted: %q", answer)
		}
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
