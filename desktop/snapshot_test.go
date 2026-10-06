package main

import (
	"context"
	"errors"
	"github.com/itrunswap/Kivo/desktop/internal/tray"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	bridge "github.com/itrunswap/Kivo/internal/desktop"
)

func TestOverviewReadCoalescesAndInvalidates(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":{"core":{"state":"stopped"}}}`))
	}))
	defer server.Close()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(c *config.Config) error { c.Web.Listen = strings.TrimPrefix(server.URL, "http://"); return nil }); err != nil {
		t.Fatal(err)
	}
	d := &Desktop{bridge: bridge.New(paths)}
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if r := d.readOverview(context.Background()); r.Error != "" || r.Status != 200 {
				t.Error(r)
			}
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate HTTP reads", calls.Load())
	}
	done := d.beginOperation()
	done()
	d.readOverview(context.Background())
	if calls.Load() != 2 {
		t.Fatal("mutation failed to invalidate cache", calls.Load())
	}
}

func TestTrayRetryIsBoundedAndUnavailableCannotHide(t *testing.T) {
	d := &Desktop{trayRetry: make(chan struct{}, 1)}
	for i := 0; i < 10; i++ {
		if err := d.RetryTray(); err != "" {
			t.Fatal(err)
		}
	}
	if len(d.trayRetry) != 1 {
		t.Fatal("unbounded retry queue")
	}
	d.trayStarting = true
	if d.RetryTray() == "" {
		t.Fatal("initialization accepted retry")
	}
	if d.HideWindow() == "" {
		t.Fatal("unavailable tray hid window")
	}
}

func TestInitialTrayFailureRetriesFiveTimesThenWaitsForUser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Desktop{trayRetry: make(chan struct{}, 1)}
	attempts := make(chan struct{}, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.manageTray(ctx, func(context.Context, tray.Callbacks) (tray.Driver, error) {
			attempts <- struct{}{}
			return nil, errors.New("host absent")
		}, func(int) time.Duration { return time.Millisecond })
	}()
	for i := 0; i < 5; i++ {
		select {
		case <-attempts:
		case <-time.After(time.Second):
			t.Fatal("retry stalled")
		}
	}
	select {
	case <-attempts:
		t.Fatal("automatic retry not bounded")
	case <-time.After(20 * time.Millisecond):
	}
	d.mu.RLock()
	reason := d.trayError
	ready := d.trayReady
	d.mu.RUnlock()
	if ready || !strings.Contains(reason, "host absent") {
		t.Fatal(ready, reason)
	}
	if err := d.RetryTray(); err != "" {
		t.Fatal(err)
	}
	select {
	case <-attempts:
	case <-time.After(time.Second):
		t.Fatal("manual retry ignored")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle did not stop")
	}
}
