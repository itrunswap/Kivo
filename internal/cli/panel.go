package cli

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/core"
)

// refreshPanel 每次进入输入状态时刷新；逐键重绘只读取快照，不发送外网请求。
func (s *Shell) refreshPanel(ctx context.Context) {
	if err := s.reloadConnection(); err != nil {
		s.panelOverview = nil
		s.panelError = "本地连接配置读取失败 · 请检查 config.json"
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	overview, err := s.client.Overview(ctx)
	if err != nil {
		s.panelOverview = nil
		s.panelError = "控制服务未连接 · /web start 启动，/web status 检查"
		return
	}
	s.panelOverview, s.panelError = &overview, ""
}

// panelSpan 在裁剪之前保留文字与样式的边界，避免截断 ANSI 序列或把转义码当作列宽。
type panelSpan struct {
	text  string
	style string
}

type panelBadge struct {
	text  string
	style string
}

// renderStatusPanel 只渲染本地快照，不读盘、不调用 API，更不会逐键触发联网检测。
// 宽度取终端安全宽度（最后一列不使用），长节点名裁剪但始终保留右边框。
func (s *Shell) renderStatusPanel(width int, styled bool) []string {
	width = max(1, width)
	inner := max(0, width-4) // 左边框、左右留白、右边框。
	span := func(text, style string) panelSpan { return panelSpan{text, style} }
	rows := make([][]panelSpan, 6)
	footer := "下一步 /web start"
	if s.panelOverview == nil {
		rows[0] = []panelSpan{span("○ 控制服务未连接", yellow+bold)}
		rows[1] = []panelSpan{span("无法确认代理状态；不等同于已停止", yellow)}
		rows[2] = []panelSpan{span(valueOr(s.panelError, "等待本地状态 · 输入 / 查看命令"), dim)}
		rows[3] = []panelSpan{span("检查 /web status", cyan)}
		rows[4] = []panelSpan{span("启动 /web start", cyan)}
		rows[5] = []panelSpan{span("帮助 /help · 不会自动更改系统代理", dim)}
	} else {
		o := s.panelOverview
		conclusion := app.SummarizeConnection(*o)
		style := panelLevelStyle(conclusion.Level)
		marker := "○ "
		if conclusion.Level == "ok" {
			marker = "● "
		} else if conclusion.Level == "error" || conclusion.Level == "warning" {
			marker = "! "
		}
		title := conclusion.Title
		if conclusion.Level == "ok" {
			title = "代理已启用 · 外网检测通过"
		}
		if o.Core.State == core.StateStopped && o.SystemProxy.State == "this_app" {
			// 系统仍指向一个关闭的端口时，不用绿色“已接入”掩盖可能断网的风险。
			title, marker, style = "代理已停止 · 系统仍指向本程序", "! ", yellow
		}
		rows[0] = []panelSpan{span(marker+title, style+bold)}

		// 双列分别分配宽度，防止左边的长状态挤掉右边的系统接入状态。
		service, system := panelServiceBadge(*o, inner < 58), panelSystemBadge(o.SystemProxy.State)
		if o.SystemProxy.Managed {
			system = panelBadge{"● 自动接入", green}
		} else if o.SystemProxy.Supported && o.SystemProxy.State == "this_app" {
			system = panelBadge{"● 手动接入", green}
		}
		if o.SystemProxy.RecoveryPending {
			system = panelBadge{"! 备份待恢复", yellow}
		}
		if o.SystemProxy.State == "this_app" && (o.Core.State != core.StateRunning || !o.ProxyPortListening) {
			system = panelBadge{"! 接入未就绪", yellow}
		}
		leftWidth := max(0, (inner-3)/2)
		left := fitPanelSpans([]panelSpan{span("服务 ", dim), span(service.text, service.style)}, leftWidth)
		rows[1] = append(left, span(" │ ", dim), span("系统 ", dim), span(system.text, system.style))
		rows[2] = panelNetworkSpans(o.Connectivity, inner)
		if o.Core.State != core.StateRunning || !o.ProxyPortListening {
			rows[2] = []panelSpan{span("联网 ", dim), span("— 服务未就绪", dim)}
		}

		node := "—（服务未运行）"
		if o.Core.State == core.StateRunning {
			node = valueOr(o.Core.EffectiveNode, valueOr(o.Core.CurrentNode, "未选择 · /node list"))
		}
		rows[3] = []panelSpan{span("节点 ", dim), span(node, cyan)}
		mode := map[string]string{"rule": "规则分流", "global": "全局代理", "direct": "全部直连"}[strings.ToLower(o.Core.Mode)]
		tun := "TUN 关闭"
		if o.TUNEnabled {
			tun = "TUN 已配置/未验证"
		}
		rows[4] = []panelSpan{span("入口 ", dim), span(fmt.Sprintf("127.0.0.1:%d", o.Core.MixedPort), ""), span(" · "+valueOr(mode, "模式未知")+" · "+tun, dim)}
		rows[5] = []panelSpan{span("订阅 ", dim), span(fmt.Sprintf("%d/%d 已启用", o.EnabledCount, o.SubscriptionCount), ""), span(" · Mihomo "+valueOr(o.Core.Version, "未安装"), dim)}
		footer = "下一步 " + conclusion.NextCommand
	}
	lines := make([]string, 0, suggestionViewportRows)
	lines = append(lines, s.renderPanelSpans(panelFrameSpans(width, "KIVO · 连接概览", true), styled))
	for _, row := range rows {
		if width < 4 {
			lines = append(lines, s.renderPanelSpans(fitPanelSpans(row, width), styled))
			continue
		}
		line := append([]panelSpan{span("│ ", dim)}, fitPanelSpans(row, inner)...)
		line = append(line, span(" │", dim))
		lines = append(lines, s.renderPanelSpans(line, styled))
	}
	lines = append(lines, s.renderPanelSpans(panelFrameSpans(width, footer, false), styled))
	return lines
}

func panelLevelStyle(level string) string {
	switch level {
	case "ok":
		return green
	case "error":
		return red
	case "warning":
		return yellow
	default:
		return dim
	}
}

func panelServiceBadge(o app.Overview, compact bool) panelBadge {
	state := map[core.State]string{core.StateNotInstalled: "未安装", core.StateStopped: "已停止", core.StateStarting: "启动中", core.StateStopping: "停止中", core.StateFailed: "异常"}[o.Core.State]
	if o.Core.State == core.StateRunning {
		if !o.ProxyPortListening {
			return panelBadge{"! 端口未监听", red}
		}
		if compact {
			return panelBadge{"● 运行中", green}
		}
		return panelBadge{"● 运行中 · 正在监听", green}
	}
	if o.Core.State == core.StateFailed {
		return panelBadge{"× " + state, red}
	}
	if o.Core.State == core.StateStarting || o.Core.State == core.StateStopping {
		return panelBadge{"! " + state, yellow}
	}
	return panelBadge{"○ " + valueOr(state, "未知"), dim}
}

func panelSystemBadge(state string) panelBadge {
	switch state {
	case "this_app":
		return panelBadge{"● 已接入", green}
	case "off":
		return panelBadge{"○ 未接入", yellow}
	case "other":
		return panelBadge{"! 指向其他代理", yellow}
	case "automatic":
		return panelBadge{"? PAC 待确认", yellow}
	default:
		return panelBadge{"? 未确认", yellow}
	}
}

// panelNetworkSpans 把未检测、失效与真正失败分开；旧结果不能继续显示绿色通过。
func panelNetworkSpans(report *app.ConnectivityReport, width int) []panelSpan {
	result := []panelSpan{{"联网 ", dim}}
	if report == nil {
		return append(result, panelSpan{"○ 未检测 · /proxy check", yellow})
	}
	if !report.Fresh() {
		return append(result, panelSpan{"! 已失效 · /proxy check", yellow})
	}
	states := map[string]string{}
	for _, route := range report.Routes {
		states[route.ID] = route.State
	}
	badge := func(label, state string) panelSpan {
		text, style := "未检测", yellow
		switch state {
		case "ok":
			text, style = "通过", green
		case "partial":
			text = "部分通过"
		case "failed":
			text, style = "未通过", red
		}
		return panelSpan{label + text, style}
	}
	result = append(result, badge("入口", states["entry"]), panelSpan{" · ", dim}, badge("节点", states["node"]))
	// 小窗口优先完整保留两条检测路径，大窗口补上检测时间。
	if width >= 53 {
		result = append(result, panelSpan{" · " + report.CheckedAt.Local().Format("15:04:05") + " 检测", dim})
	}
	return result
}

// fitPanelSpans 先按显示列数裁剪纯文字，再附加颜色；最后补齐空格以保持边框对齐。
func fitPanelSpans(spans []panelSpan, width int) []panelSpan {
	var plain strings.Builder
	safeSpans := make([]panelSpan, 0, len(spans))
	for _, span := range spans {
		// 节点名和错误消息可能来自外部数据。控制字符不得制造额外行或注入终端指令。
		span.text = strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, span.text)
		safeSpans = append(safeSpans, span)
		plain.WriteString(span.text)
	}
	clipped := []rune(truncateDisplay(plain.String(), width))
	result := make([]panelSpan, 0, len(spans)+1)
	position := 0
	for _, span := range safeSpans {
		if position >= len(clipped) {
			break
		}
		end := min(position+len([]rune(span.text)), len(clipped))
		result = append(result, panelSpan{string(clipped[position:end]), span.style})
		position = end
	}
	if padding := width - displayWidth(string(clipped)); padding > 0 {
		result = append(result, panelSpan{strings.Repeat(" ", padding), ""})
	}
	return result
}

func panelBorder(width int, label string, top bool) string {
	if width < 6 {
		return strings.Repeat("─", width)
	}
	left, right := "╰", "╯"
	if top {
		left, right = "╭", "╮"
	}
	prefix := left + "─ " + truncateDisplay(label, width-6) + " "
	return prefix + strings.Repeat("─", max(0, width-displayWidth(prefix)-1)) + right
}

// panelFrameSpans 让边框退后、标题与下一步命令突出，不对整行使用高亮色。
func panelFrameSpans(width int, label string, top bool) []panelSpan {
	border := panelBorder(width, label, top)
	if width < 6 {
		return []panelSpan{{border, dim}}
	}
	prefix, style := "╰─ ", cyan
	if top {
		prefix, style = "╭─ ", purple+bold
	}
	label = truncateDisplay(label, width-6)
	suffix := strings.TrimPrefix(strings.TrimPrefix(border, prefix), label)
	return []panelSpan{{prefix, dim}, {label, style}, {suffix, dim}}
}

func (s *Shell) renderPanelSpans(spans []panelSpan, styled bool) string {
	var result strings.Builder
	for _, span := range spans {
		if styled {
			result.WriteString(s.paint(span.style, span.text))
		} else {
			result.WriteString(span.text)
		}
	}
	return result.String()
}
