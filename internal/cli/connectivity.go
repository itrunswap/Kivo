package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/itrunswap/Kivo/internal/app"
)

func (s *Shell) checkConnectivity(ctx context.Context) error {
	fmt.Fprintln(s.out, "正在检测 Google / Cloudflare 的 HTTPS 连通性，通常需要 10–20 秒…")
	report, err := s.client.CheckConnectivity(ctx)
	if err != nil {
		return err
	}
	s.printConnectivity(report)
	s.refreshPanel(ctx)
	if s.panelOverview != nil {
		conclusion := app.SummarizeConnection(*s.panelOverview)
		fmt.Fprintf(s.out, "\n  %s\n  %s\n  下一步：%s\n", conclusion.Title, conclusion.Detail, conclusion.NextCommand)
	}
	if !report.Fresh() {
		return errors.New("检测结果已失效，请重新检测")
	}
	for _, route := range report.Routes {
		if route.ID != "direct" && route.State != "ok" {
			return errors.New("代理联网检测未全部通过，详见上方结果")
		}
	}
	return nil
}

// printConnectivity 不输出请求 URL、响应正文或任何内部认证信息。
func (s *Shell) printConnectivity(report app.ConnectivityReport) {
	fmt.Fprintf(s.out, "\n  检测时间 %s · 模式 %s · PROXY → %s\n", report.CheckedAt.Local().Format("2006-01-02 15:04:05"), report.Mode, valueOr(report.Node, "未确认"))
	states := map[string]string{"ok": "通过", "partial": "部分通过", "failed": "未通过", "skipped": "未检测"}
	for _, route := range report.Routes {
		fmt.Fprintf(s.out, "\n  %s：%s\n  %s\n", route.Label, valueOr(states[route.State], "未确认"), route.Message)
		for _, probe := range route.Probes {
			fmt.Fprintf(s.out, "    %-12s %s · %d ms · %s\n", probe.Target, states[probe.State], probe.DurationMS, probe.Message)
		}
	}
	fmt.Fprintln(s.out, "\n  仅检测服务所在主机到测试站点；不保证所有网站、浏览器扩展或应用均走代理。")
	if report.Stale {
		fmt.Fprintln(s.out, "  注意："+report.StaleReason)
	}
}

func (s *Shell) proxySetup(ctx context.Context) error {
	guide, err := s.client.ProxySetup(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "\n  代理接入指引 · %s · %s:%d\n", guide.Platform, guide.Address, guide.Port)
	for i, step := range guide.Steps {
		fmt.Fprintf(s.out, "  %d. %s\n", i+1, step)
	}
	fmt.Fprintf(s.out, "\n  如何停用：%s\n\n  注意：%s\n  本命令只显示指引，不修改系统设置。\n", guide.Disable, guide.Warning)
	return nil
}
