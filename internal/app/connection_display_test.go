package app

import (
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestDisplayConnectionRequiresNodeAndEntryEvidence(t *testing.T) {
	now := time.Now()
	base := Overview{Core: core.Status{State: core.StateRunning, Mode: "rule"}, ProxyPortListening: true, SystemProxy: platform.SystemProxyStatus{Supported: true, State: "this_app"}}
	for _, tt := range []struct {
		name, entry, node, tone string
		age                     time.Duration
		stale                   bool
	}{
		{"both", "ok", "ok", "good", 0, false},
		{"entry only", "ok", "skipped", "warn", 0, false},
		{"node failed", "ok", "failed", "warn", 0, false},
		{"partial", "partial", "ok", "warn", 0, false},
		{"failed", "failed", "ok", "error", 0, false},
		{"expired", "ok", "ok", "active", 3 * time.Minute, false},
		{"future", "ok", "ok", "active", -time.Minute, false},
		{"changed", "ok", "ok", "active", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			o.Connectivity = &ConnectivityReport{CheckedAt: now.Add(-tt.age), Stale: tt.stale, Routes: []ConnectivityRoute{{ID: "entry", State: tt.entry}, {ID: "node", State: tt.node}}}
			v := DisplayConnection(o, now)
			if v.Tone != tt.tone || !v.On {
				t.Fatalf("unexpected display: %#v", v)
			}
			if tt.tone == "good" {
				o.Core.Mode = "direct"
				if DisplayConnection(o, now).Tone == "good" {
					t.Fatal("direct mode claims proxy verified")
				}
			}
		})
	}
	base.SystemProxy.RecoveryPending = true
	if v := DisplayConnection(base, now); v.CanConnect || v.Tone != "warn" {
		t.Fatal(v)
	}
}
