package cli

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestStatusPanelSeparatesProcessFromProxyConfiguration(t *testing.T) {
	s := &Shell{panelOverview: &app.Overview{Core: core.Status{State: core.StateRunning, MixedPort: 17890, CurrentNode: "测试节点", Mode: "rule"}, ProxyPortListening: true, SystemProxy: platform.SystemProxyStatus{State: "off", Message: "系统代理未开启"}, SubscriptionCount: 2}}
	lines := s.renderStatusPanel(s.editorContentWidth(), false)
	if len(lines) != suggestionViewportRows {
		t.Fatal("panel must retain the fixed viewport")
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"运行中", "正在监听", "系统", "○ 未接入", "测试节点", "TUN 关闭", "/proxy check", "外网待检测"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s: %s", want, text)
		}
	}
	s.panelOverview.Core.State = core.StateNotInstalled
	s.panelOverview.ProxyPortListening = false
	text = strings.Join(s.renderStatusPanel(s.editorContentWidth(), false), "\n")
	if strings.Contains(text, "测试节点") || !strings.Contains(text, "/install mihomo") {
		t.Fatal(text)
	}
}

func panelTestOverview() app.Overview {
	return app.Overview{
		Core:               core.Status{State: core.StateRunning, MixedPort: 17890, EffectiveNode: "[订阅] 香港-优化 02", Mode: "rule", Version: "v1.19.31"},
		ProxyPortListening: true, SystemProxy: platform.SystemProxyStatus{State: "this_app"}, EnabledCount: 1, SubscriptionCount: 2,
		Connectivity: &app.ConnectivityReport{CheckedAt: time.Now(), Routes: []app.ConnectivityRoute{{ID: "entry", State: "ok"}, {ID: "node", State: "ok"}}},
	}
}

// 每种状态都有文字和行动指引；不依赖颜色，也不把未知/TUN 配置当作接入证明。
func TestStatusCardStateMatrix(t *testing.T) {
	cases := []struct {
		name   string
		change func(*app.Overview)
		want   []string
		avoid  string
	}{
		{"ready", func(o *app.Overview) {}, []string{"代理已启用 · 外网检测通过", "● 已接入", "入口通过", "节点通过"}, ""},
		{"stopped", func(o *app.Overview) {
			o.Core.State = core.StateStopped
			o.ProxyPortListening = false
			o.Connectivity = nil
		}, []string{"代理已停止 · 系统仍指向本程序", "○ 已停止", "接入未就绪", "服务未运行", "/core start"}, "香港-优化"},
		{"not-installed", func(o *app.Overview) { o.Core.State = core.StateNotInstalled; o.Connectivity = nil }, []string{"未安装", "/install mihomo"}, "香港-优化"},
		{"failed", func(o *app.Overview) { o.Core.State = core.StateFailed; o.Connectivity = nil }, []string{"代理服务异常", "× 异常", "/logs 200"}, "外网检测通过"},
		{"starting", func(o *app.Overview) { o.Core.State = core.StateStarting; o.Connectivity = nil }, []string{"启动中", "/status"}, "外网检测通过"},
		{"stopping", func(o *app.Overview) { o.Core.State = core.StateStopping; o.Connectivity = nil }, []string{"停止中", "/status"}, "外网检测通过"},
		{"no-listener", func(o *app.Overview) { o.ProxyPortListening = false; o.Connectivity = nil }, []string{"代理入口未就绪", "端口未监听", "/logs 200"}, "正在监听"},
		{"no-check", func(o *app.Overview) { o.Connectivity = nil }, []string{"外网待检测", "○ 未检测", "/proxy check"}, "入口通过"},
		{"expired", func(o *app.Overview) { o.Connectivity.CheckedAt = time.Now().Add(-3 * time.Minute) }, []string{"外网待检测", "已失效", "/proxy check"}, "入口通过"},
		{"changed", func(o *app.Overview) { o.Connectivity.Stale = true }, []string{"已失效", "/proxy check"}, "入口通过"},
		{"entry-failed", func(o *app.Overview) { o.Connectivity.Routes[0].State = "failed" }, []string{"外网检测未通过", "入口未通过", "节点通过"}, "系统已接入 · 外网检测通过"},
		{"node-partial", func(o *app.Overview) { o.Connectivity.Routes[1].State = "partial" }, []string{"外网部分可达", "节点部分通过"}, "系统已接入 · 外网检测通过"},
		{"node-failed", func(o *app.Overview) { o.Connectivity.Routes[1].State = "failed" }, []string{"节点出口未通过", "节点未通过", "/node list"}, "系统已接入 · 外网检测通过"},
		{"system-off", func(o *app.Overview) { o.SystemProxy.State = "off" }, []string{"系统接入待确认", "○ 未接入", "/proxy setup"}, "● 已接入"},
		{"system-other", func(o *app.Overview) { o.SystemProxy.State = "other" }, []string{"指向其他代理", "/proxy setup"}, "● 已接入"},
		{"system-pac", func(o *app.Overview) { o.SystemProxy.State = "automatic" }, []string{"PAC 待确认", "/proxy setup"}, "● 已接入"},
		{"system-unknown", func(o *app.Overview) { o.SystemProxy.State = "unknown" }, []string{"? 未确认", "/proxy setup"}, "● 已接入"},
		{"tun-unverified", func(o *app.Overview) { o.TUNEnabled = true; o.SystemProxy.State = "off" }, []string{"系统接入待确认", "TUN 已配置/未验证"}, "系统已接入 · 外网检测通过"},
		{"direct", func(o *app.Overview) { o.Core.Mode = "direct" }, []string{"当前为直连模式", "全部直连", "/mode rule"}, "系统已接入 · 外网检测通过"},
		{"global", func(o *app.Overview) { o.Core.Mode = "global" }, []string{"全局代理", "外网检测通过"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := panelTestOverview()
			tc.change(&o)
			s := &Shell{panelOverview: &o}
			lines := s.renderStatusPanel(99, false)
			if len(lines) != suggestionViewportRows {
				t.Fatalf("unexpected panel height: %d", len(lines))
			}
			text := strings.Join(lines, "\n")
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q:\n%s", want, text)
				}
			}
			if tc.avoid != "" && strings.Contains(text, tc.avoid) {
				t.Fatalf("misleading %q:\n%s", tc.avoid, text)
			}
			if tc.name != "ready" && tc.name != "global" && strings.Contains(text, "代理已启用 · 外网检测通过") {
				t.Fatalf("must not imply enabled and connected:\n%s", text)
			}
		})
	}
}

func TestUnavailableServiceMasksEvenFreshOldSuccess(t *testing.T) {
	for _, state := range []core.State{core.StateStopped, core.StateStarting, core.StateStopping, core.StateFailed, core.StateNotInstalled} {
		o := panelTestOverview()
		o.Core.State = state
		s := &Shell{color: true, panelOverview: &o}
		text := strings.Join(s.renderStatusPanel(99, false), "\n")
		for _, misleading := range []string{"入口通过", "节点通过", "● 已接入", "代理已启用"} {
			if strings.Contains(text, misleading) {
				t.Errorf("%s retained misleading status %q:\n%s", state, misleading, text)
			}
		}
		if !strings.Contains(text, "联网 — 服务未就绪") {
			t.Fatal(text)
		}
	}
}

func TestStatusCardWidthAndColorHaveIdenticalLayout(t *testing.T) {
	o := panelTestOverview()
	o.Core.EffectiveNode = strings.Repeat("🇭🇰 香港长节点名称 e\u0301 ", 20)
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, connected := range []bool{true, false} {
		for _, width := range []int{1, 2, 3, 4, 5, 6, 20, 32, 48, 60, 79, 99, 139} {
			t.Run(fmt.Sprintf("connected=%t/width=%d", connected, width), func(t *testing.T) {
				s := &Shell{color: true, panelError: "控制服务未连接"}
				if connected {
					s.panelOverview = &o
				}
				plain, colored := s.renderStatusPanel(width, false), s.renderStatusPanel(width, true)
				for index, line := range plain {
					if displayWidth(line) != width {
						t.Errorf("row %d must fill exactly %d columns, got %d: %q", index, width, displayWidth(line), line)
					}
					if decoded := ansi.ReplaceAllString(colored[index], ""); decoded != line {
						t.Errorf("color changed layout: %q != %q", decoded, line)
					}
					if strings.Contains(line, "\x1b") {
						t.Fatal("plain output must not contain ANSI")
					}
					if width >= 4 && index > 0 && index < len(plain)-1 && !strings.HasSuffix(line, " │") {
						t.Fatalf("right border was truncated: %q", line)
					}
				}
				s.color = false
				if got := strings.Join(s.renderStatusPanel(width, true), "\n"); got != strings.Join(plain, "\n") {
					t.Fatal("NO_COLOR must retain identical text")
				}
			})
		}
	}
}

func TestCompactCardKeepsIndependentCriticalStatuses(t *testing.T) {
	o := panelTestOverview()
	o.SystemProxy.State = "off"
	s := &Shell{panelOverview: &o}
	text := strings.Join(s.renderStatusPanel(48, false), "\n")
	for _, want := range []string{"运行中", "未接入", "入口通过", "节点通过", "/proxy setup"} {
		if !strings.Contains(text, want) {
			t.Errorf("compact card lost %q:\n%s", want, text)
		}
	}
}

func TestStatusCardTreatsExternalControlCharactersAsData(t *testing.T) {
	o := panelTestOverview()
	o.Core.EffectiveNode = "节点\n伪造状态\r\x1b[2J\t名称"
	s := &Shell{panelOverview: &o}
	for _, line := range s.renderStatusPanel(79, false) {
		if strings.ContainsAny(line, "\n\r\t\x1b") || displayWidth(line) != 79 {
			t.Fatalf("external text broke the card: %q", line)
		}
	}
}

func TestEditorKeepsStatusCardAndSuggestionsInSameEightRows(t *testing.T) {
	o := panelTestOverview()
	var out bytes.Buffer
	s := &Shell{out: &out, color: true, panelOverview: &o}
	rows := s.redrawEditor("", 0, nil, 0, 0)
	if rows != editorRowsAboveInput || !strings.Contains(out.String(), "连接概览") {
		t.Fatal(out.String())
	}
	out.Reset()
	if got := s.redrawEditor("/", 1, matchingSuggestions("/", 0), 20, rows); got != rows {
		t.Fatal("suggestions moved input row")
	}
	if strings.Contains(out.String(), "连接概览") || !strings.Contains(out.String(), "命令 ") {
		t.Fatal("suggestions must replace the card, not append below it")
	}
	out.Reset()
	s.redrawEditor("", 0, nil, 0, rows)
	if !strings.Contains(out.String(), "连接概览") || strings.Contains(out.String(), "命令 1-") {
		t.Fatal("clearing input must restore the status card")
	}
}

// 预览使用虚构数据，不读取真实订阅、Token 或系统代理，也不启动控制服务。
// 设置 KIVO_PANEL_PREVIEW=1 后，go test -run TestPanelPreview -v 可查看设计样例。
func TestPanelPreview(t *testing.T) {
	if os.Getenv("KIVO_PANEL_PREVIEW") != "1" {
		t.Skip("explicit visual preview only")
	}
	for _, name := range []string{"ready", "not-connected", "pending", "stopped", "failed"} {
		o := panelTestOverview()
		switch name {
		case "not-connected":
			o.SystemProxy.State = "off"
		case "pending":
			o.Connectivity = nil
		case "stopped":
			o.Core.State = core.StateStopped
			o.Connectivity = nil
			o.ProxyPortListening = false
		case "failed":
			o.Connectivity.Routes[1].State = "failed"
		}
		fmt.Println("\n" + name)
		s := &Shell{color: os.Getenv("NO_COLOR") == "", panelOverview: &o}
		for _, line := range s.renderStatusPanel(79, true) {
			fmt.Println(line)
		}
	}
}

func TestFinishEditorDoesNotRepeatPanelsOrBorders(t *testing.T) {
	var out bytes.Buffer
	s := &Shell{out: &out}
	s.finishEditor("/status", editorRowsAboveInput)
	if strings.Contains(out.String(), "─") || strings.Count(out.String(), "/status") != 1 {
		t.Fatal(out.String())
	}
}

func TestPlainPanelTextDoesNotGenerateMalformedANSI(t *testing.T) {
	s := &Shell{color: true}
	if got := s.paint("", "端口"); got != "端口" {
		t.Fatalf("empty style must produce plain text: %q", got)
	}
}

func TestNonTerminalProgressIsThrottledAndHasNoEscapeSequences(t *testing.T) {
	var out bytes.Buffer
	s := &Shell{out: &out}
	p := s.newInstallProgress()
	for n := int64(0); n <= 100; n++ {
		p.update(core.InstallEvent{Stage: "download", Downloaded: n, Total: 100})
	}
	p.finish()
	if strings.Contains(out.String(), "\x1b") || strings.Count(out.String(), "\n") != 11 || !strings.Contains(out.String(), "100%") {
		t.Fatal(out.String())
	}
}

func TestCompactNodeOutputFitsTerminalColumnsAndDoesNotClaimUntestedAlive(t *testing.T) {
	var out bytes.Buffer
	s := &Shell{out: &out}
	nodes := []core.Node{{Name: "[订阅] 🇭🇰 香港测试长节点名称-谷歌学术-VIP", Type: "Shadowsocks", ProviderName: "订阅测试", Alive: true}}
	if err := s.printNodes(nodes, ""); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if displayWidth(line) > s.editorContentWidth() {
			t.Fatalf("wrapped line: %q", line)
		}
	}
	if !strings.Contains(out.String(), "未测速") || strings.Contains(out.String(), "检查通过") {
		t.Fatal(out.String())
	}
	row := nodeTableRow("1", nodes[0].Name, "Shadowsocks", "订阅测试", "100 ms", "检查通过")
	if displayWidth(row) > 101 {
		t.Fatalf("wide table overflow: %q", row)
	}
}
