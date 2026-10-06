package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/core"
)

func TestSubscriptionResultShowsCountsNotOnlyProviderTotal(t *testing.T) {
	var out bytes.Buffer
	shell := NewShell(nil, strings.NewReader(""), &out, "", "")
	count, previous := 219, 215
	shell.printSubscriptionResults([]app.SubscriptionActionItem{{Name: "example", Via: "direct", NodeCount: &count, PreviousCount: &previous, DurationMS: 1234}, {Name: "failed", Via: "proxy", Error: "unavailable"}})
	for _, want := range []string{"219 个节点", "更新前 215", "直连", "1.2s", "PROXY 出站", "未确认更新结果"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
}

func TestNodeListIdentifiesOfflineCache(t *testing.T) {
	var out bytes.Buffer
	shell := NewShell(nil, strings.NewReader(""), &out, "", "")
	if err := shell.printNodes([]core.Node{{Name: "cached-node", Cached: true}}, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "离线缓存") || !strings.Contains(out.String(), "/core start") {
		t.Fatal(out.String())
	}
}
