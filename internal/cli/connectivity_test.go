package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestProxyCommandsUseTypedAPIAndDoNotImplySystemMutation(t *testing.T) {
	checks, mutations := 0, 0
	report := app.ConnectivityReport{CheckedAt: time.Now(), Routes: []app.ConnectivityRoute{{ID: "direct", Label: "本机直连", State: "failed"}, {ID: "entry", Label: "日常代理入口", State: "ok"}, {ID: "node", Label: "选中节点出口", State: "ok"}}}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/overview":
			writeTestEnvelope(t, w, app.Overview{Core: core.Status{State: core.StateRunning, MixedPort: 17890, Mode: "rule"}, ProxyPortListening: true, Connectivity: &report, SystemProxy: platform.SystemProxyStatus{State: "off"}})
		case "/api/v1/connectivity/check":
			if r.Method != http.MethodPost {
				t.Error("check must use POST")
			}
			checks++
			writeTestEnvelope(t, w, report)
		case "/api/v1/proxy/setup":
			writeTestEnvelope(t, w, platform.SystemProxyGuide(17890))
		case "/api/v1/core/stop":
			mutations++
			writeTestEnvelope(t, w, core.Status{State: core.StateStopped})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()
	var out bytes.Buffer
	s := NewShell(client.New(strings.TrimPrefix(controller.URL, "http://"), "test"), strings.NewReader(""), &out, controller.URL, "test")
	for _, args := range [][]string{{"proxy"}, {"proxy", "status"}, {"proxy", "setup"}} {
		if err := s.Execute(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	if checks != 0 || mutations != 0 {
		t.Fatal("status/setup must be read-only")
	}
	if !strings.Contains(out.String(), "不修改系统设置") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := s.Execute(context.Background(), []string{"proxy", "check"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"本机直连", "日常代理入口", "选中节点出口", "系统接入待确认"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(out.String())
		}
	}
	// 单个目标/路径失败必须通过非零错误暴露给普通 CLI 调用方。
	report.Routes[2].State = "partial"
	if err := s.Execute(context.Background(), []string{"proxy", "check"}); err == nil {
		t.Fatal("partial check must fail")
	}
	out.Reset()
	if err := s.Execute(context.Background(), []string{"proxy", "off"}); err != nil {
		t.Fatal(err)
	}
	if mutations != 1 || !strings.Contains(out.String(), "恢复本程序管理的系统代理") || !strings.Contains(out.String(), "避免断网") {
		t.Fatal(out.String())
	}
}

func TestConnectivityPanelExpiresWithoutAnotherNetworkRequest(t *testing.T) {
	r := &app.ConnectivityReport{CheckedAt: time.Now().Add(-3 * time.Minute), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}, {ID: "node", State: "ok"}}}
	o := panelTestOverview()
	o.Connectivity = r
	s := &Shell{panelOverview: &o}
	if text := strings.Join(s.renderStatusPanel(79, false), "\n"); !strings.Contains(text, "已失效") || strings.Contains(text, "入口通过") {
		t.Fatal(text)
	}
}
