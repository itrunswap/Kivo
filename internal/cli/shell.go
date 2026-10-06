// Package cli 实现 Codex 风格的交互式斜杠命令终端。
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
	"github.com/itrunswap/Kivo/internal/version"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[38;5;245m"
	purple = "\x1b[38;5;141m"
	cyan   = "\x1b[38;5;80m"
	green  = "\x1b[38;5;78m"
	yellow = "\x1b[38;5;221m"
	red    = "\x1b[38;5;203m"
)

// Shell 是交互式终端会话。
type Shell struct {
	client              *client.Client
	input               io.Reader
	reader              *bufio.Reader
	out                 io.Writer
	webURL              string
	secret              string
	color               bool
	interactive         bool // 最近一次输入是否使用真实终端编辑器。
	startWeb            func(context.Context) error
	refreshConnection   func() (*client.Client, string, string, error)
	offlineShutdown     func() error
	offlineSystemProxy  func(context.Context, string, bool) (app.ProxyOperationResult, error)
	cachedNodes         []core.Node
	cachedSubscriptions []app.PublicSubscription
	history             []string
	panelOverview       *app.Overview
	panelError          string
}

// NewShell 创建会话。
func NewShell(api *client.Client, input io.Reader, output io.Writer, webURL, secret string) *Shell {
	return &Shell{client: api, input: input, reader: bufio.NewReader(input), out: output, webURL: webURL, secret: secret, color: os.Getenv("NO_COLOR") == ""}
}

// SetWebStarter 注入后台控制服务启动函数。CLI 只依赖函数，不感知平台进程细节。
func (s *Shell) SetWebStarter(start func(context.Context) error) { s.startWeb = start }

// SetConnectionRefresher 在执行命令前读取当前本地连接配置，避免 Web 修改
// Token 后长驻 CLI 仍使用旧密钥。函数只在命令边界调用，不在逐键重绘时读盘。
func (s *Shell) SetConnectionRefresher(refresh func() (*client.Client, string, string, error)) {
	s.refreshConnection = refresh
}

func (s *Shell) reloadConnection() error {
	if s.refreshConnection == nil {
		return nil
	}
	api, address, secret, err := s.refreshConnection()
	if err != nil {
		return err
	}
	s.client, s.webURL, s.secret = api, address, secret
	return nil
}

// SetOfflineShutdown 注入 Web 已停止时的持久化关闭动作。
// 此时 Core 不可能仍由控制服务托管，只需清除下次启动偏好，无需为关闭而短暂启动服务。
func (s *Shell) SetOfflineShutdown(shutdown func() error) { s.offlineShutdown = shutdown }

// SetOfflineSystemProxy 允许后台停止后独立查询/恢复代理，不为恢复动作启动内核。
func (s *Shell) SetOfflineSystemProxy(action func(context.Context, string, bool) (app.ProxyOperationResult, error)) {
	s.offlineSystemProxy = action
}

// Run 进入交互循环。/quit 只退出 CLI，不停止后台代理。
func (s *Shell) Run(ctx context.Context) error {
	if out, ok := s.outputFile(); ok {
		restore, _ := platform.EnableTerminalOutput(out)
		defer restore()
	}
	s.banner(ctx)
	for {
		s.refreshPanel(ctx)
		line, err := s.readInteractiveLine(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, errShellExit) {
			fmt.Fprintln(s.out, s.paint(dim, "已退出终端，后台代理保持当前状态。"))
			return nil
		}
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		s.recordHistory(line)
		args, err := SplitArgs(strings.TrimPrefix(line, "/"))
		if err != nil {
			s.error(err)
			continue
		}
		if len(args) == 0 {
			continue
		}
		if args[0] == "quit" || args[0] == "exit" || args[0] == "q" {
			fmt.Fprintln(s.out, s.paint(dim, "已退出终端，后台代理保持当前状态。"))
			return nil
		}
		cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout(args))
		err = s.Execute(cmdCtx, args)
		cancel()
		if err != nil {
			s.error(err)
			continue
		}
		// /shutdown 成功后不再绘制下一次提示符，直接结束当前交互会话。
		// 普通子命令模式会在 Execute 返回后自然退出，因此无需额外状态字段。
		if args[0] == "shutdown" {
			return nil
		}
	}
}

func commandTimeout(args []string) time.Duration {
	if args[0] == "connect" || args[0] == "system-proxy" {
		return 90 * time.Second
	}
	if args[0] == "install" {
		return 20 * time.Minute
	}
	if args[0] == "core" && len(args) > 1 {
		switch args[1] {
		case "install", "update", "import":
			return 20 * time.Minute
		}
	}
	if args[0] == "node" && len(args) > 1 && args[1] == "test" {
		return 2 * time.Minute
	}
	if args[0] == "sub" && len(args) > 1 && (args[1] == "update" || args[1] == "test") {
		return 6 * time.Minute
	}
	return 45 * time.Second
}

// Execute 执行一条已拆分命令，普通子命令模式也复用此入口。
func (s *Shell) Execute(ctx context.Context, args []string) error {
	if err := s.reloadConnection(); err != nil {
		return fmt.Errorf("读取控制服务连接配置: %w", err)
	}
	switch args[0] {
	case "help", "?":
		s.help()
		return nil
	case "status", "st":
		return s.status(ctx)
	case "connect", "disconnect":
		return s.connectionCommand(ctx, args[0], tail(args, 1))
	case "system-proxy":
		return s.systemProxyCommand(ctx, tail(args, 1))
	case "install":
		return s.install(ctx, tail(args, 1))
	case "core":
		return s.coreCommand(ctx, tail(args, 1))
	case "sub", "subscription":
		return s.subscriptionCommand(ctx, tail(args, 1))
	case "node":
		return s.nodeCommand(ctx, tail(args, 1))
	case "proxy":
		return s.proxyCommand(ctx, tail(args, 1))
	case "mode":
		return s.modeCommand(ctx, tail(args, 1))
	case "route":
		return s.routeCommand(ctx, tail(args, 1))
	case "port":
		return s.portCommand(ctx, tail(args, 1))
	case "tun":
		return s.toggleSetting(ctx, "tun", tail(args, 1))
	case "lan":
		return s.toggleSetting(ctx, "lan", tail(args, 1))
	case "config":
		return s.configCommand(ctx, tail(args, 1))
	case "logs":
		return s.logs(ctx, tail(args, 1))
	case "doctor":
		return s.doctor(ctx)
	case "web":
		return s.web(ctx, tail(args, 1))
	case "shutdown":
		return s.shutdown(ctx)
	case "clear":
		fmt.Fprint(s.out, "\x1b[2J\x1b[H")
		return nil
	default:
		return fmt.Errorf("未知命令 %q，输入 /help 查看帮助", args[0])
	}
}

func (s *Shell) banner(ctx context.Context) {
	fmt.Fprintln(s.out, s.paint(purple+bold, "  Kivo")+s.paint(dim, "  "+version.Version+"  /  代理控制台"))
	fmt.Fprintln(s.out)
}

func (s *Shell) help() {
	rows := [][2]string{
		{"/connect [--replace|--adopt]", "一键启动内核、接入系统代理并检测外网"},
		{"/disconnect", "安全恢复受管系统代理并停止内核"},
		{"/system-proxy status|on|off|recover", "查看、接入、恢复系统代理；recover 可显式 --force"},
		{"/status", "查看内核、节点、端口和订阅状态"}, {"/install mihomo [版本]", "安装最新或指定版本 Mihomo"},
		{"/core status|list|install|use", "查看、安装和切换代理内核"}, {"/core start|stop|restart", "控制代理内核"},
		{"/core import|uninstall|purge", "导入或删除代理内核"}, {"/sub add", "交互或直接添加订阅"},
		{"/sub list|remove", "查看或删除订阅"}, {"/sub update [目标] [--direct|--proxy]", "更新单个、分组或全部订阅"},
		{"/sub test [目标] [--direct|--proxy]", "验证订阅源并检查节点"},
		{"/node list [关键词]", "显示并筛选节点"},
		{"/node test", "测试全部节点延迟"}, {"/node use <名称>", "选择节点"},
		{"/proxy on|off|restart", "内核快捷操作；停止时恢复受管系统代理"}, {"/mode rule|global|direct", "切换运行模式"},
		{"/proxy status", "查看服务、系统接入及外网状态（/proxy 同义）"},
		{"/proxy check", "检测直连、日常代理入口和选中节点出口"}, {"/proxy setup", "查看系统/浏览器接入和停用指引"},
		{"/route profile|group|rule", "管理路由配置和规则组"}, {"/port|/tun|/lan", "管理端口、TUN 和局域网访问"},
		{"/logs [行数]", "查看最近内核日志"}, {"/doctor", "检查内核、订阅、端口和权限"},
		{"/web start|stop|status", "启动、关闭或查询 Web 控制服务"}, {"/web [--show-token]", "显示控制台地址，按需显示密钥"},
		{"/shutdown", "停止代理与 Core、关闭 Web，然后退出 CLI"},
		{"/clear", "清空终端"}, {"/quit", "退出 CLI，后台代理保持运行"},
	}
	fmt.Fprintln(s.out, "\n"+s.paint(bold, "命令"))
	for _, row := range rows {
		fmt.Fprintf(s.out, "  %-31s %s\n", s.paint(cyan, row[0]), s.paint(dim, row[1]))
	}
}

func (s *Shell) status(ctx context.Context) error {
	overview, err := s.client.Overview(ctx)
	if err != nil {
		return err
	}
	s.panelOverview, s.panelError = &overview, ""
	if !s.interactive {
		for _, line := range s.renderStatusPanel(s.editorContentWidth(), true) {
			fmt.Fprintln(s.out, line)
		}
	} else {
		fmt.Fprintln(s.out, s.paint(green, "✓ 状态已刷新"))
	}
	fmt.Fprintln(s.out, s.paint(dim, "  /proxy check 检测外网 · /proxy setup 接入/停用指引（服务所在机器）。"))
	if overview.Core.Error != "" {
		fmt.Fprintln(s.out, "  "+s.paint(red, "错误  ")+overview.Core.Error)
	}
	return nil
}

func (s *Shell) install(ctx context.Context, args []string) error {
	version := "latest"
	if len(args) > 0 && args[0] != "mihomo" {
		version = args[0]
	} else if len(args) > 1 {
		version = args[1]
	}
	fmt.Fprintln(s.out, s.paint(cyan, "  安装 Mihomo · 官方 Release"))
	renderer := s.newInstallProgress()
	defer renderer.finish()
	err := s.client.InstallCoreProgress(ctx, version, renderer.update)
	if err != nil {
		return err
	}
	renderer.finish()
	fmt.Fprintln(s.out, s.paint(green, "✓ 安装完成"))
	return nil
}
func (s *Shell) coreCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：/core status|list|install|import|use|start|stop|restart|uninstall|purge")
	}
	switch args[0] {
	case "status":
		status, err := s.client.CoreStatus(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(s.out, "  %-12s %s\n", "状态", strings.ToUpper(string(status.State)))
		fmt.Fprintf(s.out, "  %-12s %s\n", "版本", valueOr(status.Version, "未安装"))
		fmt.Fprintf(s.out, "  %-12s %d\n", "进程 ID", status.PID)
		fmt.Fprintf(s.out, "  %-12s 127.0.0.1:%d\n", "代理地址", status.MixedPort)
		fmt.Fprintf(s.out, "  %-12s %s\n", "当前节点", valueOr(status.CurrentNode, "DIRECT"))
		if status.Error != "" {
			fmt.Fprintln(s.out, s.paint(red, "  错误：")+status.Error)
		}
		return nil
	case "list":
		items, err := s.client.CoreInstallations(ctx)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(s.out, s.paint(dim, "尚未安装任何 Mihomo 版本。"))
			return nil
		}
		fmt.Fprintf(s.out, "  %-5s %-9s %-14s %s\n", "序号", "状态", "版本", "路径")
		for index, item := range items {
			state := "INSTALLED"
			if item.Active {
				state = "ACTIVE"
			}
			fmt.Fprintf(s.out, "  %-5d %-9s %-14s %s\n", index+1, state, item.Version, item.Path)
		}
		return nil
	case "install", "update":
		version := "latest"
		if len(args) > 1 {
			version = args[1]
		}
		return s.install(ctx, []string{"mihomo", version})
	case "import":
		if len(args) < 2 {
			return errors.New("用法：/core import <官方 .gz/.zip 文件路径>")
		}
		if err := s.client.ImportCore(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 本地内核已导入并设为活动版本"))
		return nil
	case "use", "switch":
		if len(args) != 2 {
			return errors.New("用法：/core use <序号|版本>")
		}
		if err := s.client.UseCore(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 活动内核已切换"))
		return nil
	case "uninstall", "remove":
		if len(args) != 2 {
			return errors.New("用法：/core uninstall <序号|版本>")
		}
		if err := s.client.RemoveCore(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 内核版本已删除"))
		return nil
	case "purge":
		if len(args) != 2 || args[1] != "--yes" {
			return errors.New("该操作会删除全部受管内核；确认请执行 /core purge --yes")
		}
		if err := s.client.PurgeCores(ctx); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 所有受管内核已卸载"))
		return nil
	case "start", "stop", "restart":
		_, err := s.client.CoreAction(ctx, args[0])
		if err == nil {
			fmt.Fprintln(s.out, s.paint(green, "✓ 内核操作完成；停止时恢复本程序管理的系统代理，其他设置不改变"))
			if args[0] == "stop" {
				fmt.Fprintln(s.out, "  若系统/浏览器仍指向本程序，请关闭或恢复原代理设置，避免断网。/proxy setup 查看指引。")
			}
		}
		return err
	default:
		return errors.New("用法：/core status|list|install|import|use|start|stop|restart|uninstall|purge")
	}
}
func (s *Shell) proxyCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return s.status(ctx)
	}
	if len(args) != 1 {
		return errors.New("用法：/proxy status|check|setup|on|off|restart")
	}
	switch args[0] {
	case "status":
		return s.status(ctx)
	case "check":
		return s.checkConnectivity(ctx)
	case "setup":
		return s.proxySetup(ctx)
	}
	actions := map[string]string{"on": "start", "off": "stop", "restart": "restart"}
	action, ok := actions[args[0]]
	if !ok {
		return errors.New("用法：/proxy status|check|setup|on|off|restart")
	}
	_, err := s.client.CoreAction(ctx, action)
	if err == nil {
		fmt.Fprintln(s.out, s.paint(green, "✓ 代理服务状态已更新；停止时恢复本程序管理的系统代理，其他设置不改变"))
		if action == "stop" {
			fmt.Fprintln(s.out, "  请关闭或恢复系统/浏览器原代理设置，避免断网。/proxy setup 查看指引。")
		} else {
			fmt.Fprintln(s.out, "  /proxy check 检测外网；/proxy setup 查看系统接入步骤。")
		}
	}
	return err
}

func (s *Shell) subscriptionCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：/sub add|list|show|edit|enable|disable|move|update|remove|group")
	}
	switch args[0] {
	case "list":
		items, err := s.client.Subscriptions(ctx)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(s.out, s.paint(dim, "还没有订阅。"))
			return nil
		}
		s.cachedSubscriptions = items
		groupFilter := ""
		if len(args) > 1 {
			groupFilter = strings.Join(args[1:], " ")
		}
		fmt.Fprintf(s.out, "  %-5s %-9s %-18s %-14s %-8s %s\n", "序号", "状态", "名称", "分组", "更新", "地址")
		for _, item := range items {
			if groupFilter != "" && !strings.EqualFold(item.Group, groupFilter) {
				continue
			}
			state := "ENABLED"
			if !item.Enabled {
				state = "DISABLED"
			}
			fmt.Fprintf(s.out, "  %-5d %-9s %-18s %-14s %-8s %s\n", item.Index, state, truncate(item.Name, 16), truncate(item.Group, 12), strings.ToUpper(item.UpdateVia), s.paint(dim, item.URL))
		}
		return nil
	case "add":
		return s.addSubscription(ctx, args[1:])
	case "show":
		if len(args) < 2 {
			return errors.New("用法：/sub show <序号|名称>")
		}
		item, err := s.subscriptionByReference(ctx, strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		fmt.Fprintf(s.out, "  名称：%s\n  分组：%s\n  状态：%s\n  地址：%s\n  认证：%s\n  更新路径：%s\n  更新：%d 秒\n  健康检查：%d 秒\n", item.Name, item.Group, onOff(item.Enabled), item.URL, item.AuthType, item.UpdateVia, item.UpdateInterval, item.HealthInterval)
		return nil
	case "enable", "disable":
		if len(args) < 2 {
			return fmt.Errorf("用法：/sub %s <序号|名称>", args[0])
		}
		enabled := args[0] == "enable"
		if err := s.client.PatchSubscription(ctx, app.SubscriptionPatch{Reference: strings.Join(args[1:], " "), Enabled: &enabled}); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 订阅状态已更新"))
		return nil
	case "move":
		if len(args) != 3 {
			return errors.New("用法：/sub move <序号|名称> <分组>")
		}
		group := args[2]
		if err := s.client.PatchSubscription(ctx, app.SubscriptionPatch{Reference: args[1], Group: &group}); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 订阅已移动"))
		return nil
	case "edit":
		return s.editSubscription(ctx, args[1:])
	case "group":
		return s.subscriptionGroupCommand(ctx, args[1:])
	case "update":
		return s.updateSubscriptionCommand(ctx, args[1:])
	case "test":
		parsed, err := parseSubscriptionUpdateArgs(args[1:])
		if err != nil {
			return err
		}
		result, err := s.client.TestSubscriptions(ctx, app.SubscriptionTestInput{
			Reference: parsed.Reference, Group: parsed.Group, Via: parsed.Via,
		})
		s.printSubscriptionResults(result.Results)
		if err != nil {
			if result.TemporarilyStartedCore {
				fmt.Fprintln(s.out, "  本次临时启动过内核；已尝试恢复停止状态，如恢复失败会在下方提示。")
			}
			return err
		}
		pathText := "保留原设置"
		if result.Via == "proxy" {
			pathText = "通过 PROXY 策略组"
		} else if result.Via == "direct" {
			pathText = "直连"
		}
		fmt.Fprintf(s.out, "%s，共 %d 个订阅，订阅下载路径：%s\n", s.paint(green, "✓ 订阅健康检查已完成"), len(result.Tested), pathText)
		fmt.Fprintln(s.out, s.paint(dim, "  节点健康检查始终通过各节点进行；--direct/--proxy 控制的是订阅下载。"))
		if result.TemporarilyStartedCore {
			fmt.Fprintln(s.out, s.paint(dim, "  Mihomo 原本未运行，本次已临时启动并在检查后恢复停止。"))
		}
		return nil
	case "remove":
		if len(args) < 2 {
			return errors.New("用法：/sub remove <名称>")
		}
		if err := s.client.RemoveSubscription(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 订阅已删除"))
		return nil
	default:
		return errors.New("用法：/sub add|list|show|edit|enable|disable|move|update|remove|group")
	}
}

func (s *Shell) updateSubscriptionCommand(ctx context.Context, args []string) error {
	input, err := parseSubscriptionUpdateArgs(args)
	if err != nil {
		return err
	}
	result, err := s.client.UpdateSubscriptions(ctx, input)
	s.printSubscriptionResults(result.Results)
	if err != nil {
		if result.TemporarilyStartedCore {
			fmt.Fprintln(s.out, "  本次临时启动过内核；已尝试恢复停止状态，如恢复失败会在下方提示。")
		}
		return err
	}
	pathText := "保留原设置"
	if result.Via == "proxy" {
		pathText = "通过 PROXY 策略组"
	} else if result.Via == "direct" {
		pathText = "直连"
	}
	fmt.Fprintf(s.out, "%s，共 %d 个订阅，更新路径：%s\n", s.paint(green, "✓ 订阅已更新"), len(result.Updated), pathText)
	if result.TemporarilyStartedCore {
		fmt.Fprintln(s.out, s.paint(dim, "  Mihomo 原本未运行，本次已临时启动并在更新后恢复停止。"))
		fmt.Fprintln(s.out, s.paint(dim, "  /node list 可查看缓存节点；使用代理请先 /core start。"))
	}
	return nil
}

func (s *Shell) printSubscriptionResults(items []app.SubscriptionActionItem) {
	for _, item := range items {
		path := "直连"
		if item.Via == "proxy" {
			path = "PROXY 出站"
		}
		mark := s.paint(green, "✓")
		if item.Error != "" {
			mark = s.paint(red, "×")
		}
		count := "节点数未知"
		if item.NodeCount != nil {
			count = fmt.Sprintf("%d 个节点", *item.NodeCount)
			if item.PreviousCount != nil {
				count += fmt.Sprintf("（更新前 %d）", *item.PreviousCount)
			}
		} else if item.Error != "" {
			count = "未确认更新结果"
		}
		fmt.Fprintf(s.out, "  %s %s · %s · %s · %.1fs\n", mark, item.Name, path, count, float64(item.DurationMS)/1000)
		if item.Warning != "" {
			fmt.Fprintln(s.out, "    "+item.Warning)
		}
	}
}

func parseSubscriptionUpdateArgs(args []string) (app.SubscriptionUpdateInput, error) {
	input := app.SubscriptionUpdateInput{Reference: "all"}
	normalized := make([]string, 0, len(args)+1)
	aliasVia := ""
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "--proxy":
			aliasVia = "proxy"
		case "--direct":
			aliasVia = "direct"
		default:
			normalized = append(normalized, arg)
		}
	}
	positional, options, err := parseCommandOptions(normalized)
	if err != nil {
		return input, err
	}
	via := strings.ToLower(strings.TrimSpace(options["via"]))
	if aliasVia != "" {
		if via != "" && via != aliasVia {
			return input, errors.New("不能同时指定不同的订阅更新路径")
		}
		via = aliasVia
	}
	input.Group, input.Via = strings.TrimSpace(options["group"]), via
	if len(positional) > 0 {
		if strings.EqualFold(positional[0], "group") {
			if len(positional) != 2 || input.Group != "" {
				return input, errors.New("用法：/sub update group <分组> [--via direct|proxy]")
			}
			input.Group = positional[1]
		} else {
			input.Reference = strings.Join(positional, " ")
		}
	}
	for key := range options {
		if key != "via" && key != "group" {
			return input, fmt.Errorf("不支持的选项 --%s", key)
		}
	}
	return input, nil
}

func (s *Shell) addSubscription(ctx context.Context, args []string) error {
	positional, options, err := parseCommandOptions(args)
	if err != nil {
		return err
	}
	if err := validateSubscriptionOptions(options, false); err != nil {
		return err
	}
	if len(positional) > 2 {
		return errors.New("用法：/sub add [名称] <URL> [--auth 类型] [--via direct|proxy]")
	}
	name, urlValue := options["name"], ""
	if len(positional) == 1 && isHTTPURL(positional[0]) {
		urlValue = positional[0]
	}
	if len(positional) >= 2 {
		name, urlValue = positional[0], positional[1]
	}
	interactive := urlValue == ""
	if name == "" {
		if interactive {
			name = s.prompt("订阅名称（可留空自动生成）")
		}
	}
	if urlValue == "" {
		urlValue = s.prompt("订阅地址")
	}
	auth := strings.ToLower(options["auth"])
	if auth == "" {
		if interactive {
			auth = strings.ToLower(s.promptDefault("认证方式 none/basic/bearer/token/age/aes", "none"))
		} else {
			auth = "none"
		}
	}
	username, secret := options["user"], ""
	if auth == "basic" {
		if username == "" {
			username = s.prompt("用户名")
		}
	}
	if auth != "none" {
		label := "密码或 Token"
		if auth == "aes" {
			label = "订阅 AES 解密密码"
		}
		secret = s.secretPrompt(label)
	}
	input := app.SubscriptionInput{Name: name, URL: urlValue, AuthType: auth, Username: username, Secret: secret, UpdateVia: options["via"], Group: options["group"], AdditionalPrefix: options["prefix"], UpdateInterval: 3600, HealthInterval: 300, HealthCheckURL: "https://www.gstatic.com/generate_204"}
	if err := s.client.AddSubscription(ctx, input); err != nil {
		return err
	}
	fmt.Fprintln(s.out, s.paint(green, "✓ 订阅已添加；如内核运行中，配置已自动应用。/sub update 更新节点。"))
	return nil
}

func (s *Shell) editSubscription(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("用法：/sub edit <序号|名称> [--name 值] [--url 地址] [--group 分组] [--prefix 前缀] [--auth 类型] [--via direct|proxy]")
	}
	reference := args[0]
	positional, options, err := parseCommandOptions(args[1:])
	if err != nil {
		return err
	}
	if err := validateSubscriptionOptions(options, true); err != nil {
		return err
	}
	if len(positional) > 0 {
		return errors.New("修改字段请使用 --name、--url、--group 等选项")
	}
	patch := app.SubscriptionPatch{Reference: reference}
	if value, ok := options["name"]; ok {
		patch.Name = &value
	}
	if value, ok := options["url"]; ok {
		patch.URL = &value
	}
	if value, ok := options["group"]; ok {
		patch.Group = &value
	}
	if value, ok := options["prefix"]; ok {
		patch.AdditionalPrefix = &value
	}
	if value, ok := options["user"]; ok {
		patch.Username = &value
	}
	if value, ok := options["via"]; ok {
		patch.UpdateVia = &value
	}
	if value, ok := options["auth"]; ok {
		patch.AuthType = &value
		if value != "none" {
			label := "新的密码或 Token"
			if strings.EqualFold(value, "aes") {
				label = "新的订阅 AES 解密密码"
			}
			secret := s.secretPrompt(label)
			patch.Secret = &secret
		}
	}
	if patch.Name == nil && patch.URL == nil && patch.Group == nil && patch.AdditionalPrefix == nil && patch.Username == nil && patch.AuthType == nil && patch.UpdateVia == nil {
		return errors.New("没有需要修改的字段")
	}
	if err := s.client.PatchSubscription(ctx, patch); err != nil {
		return err
	}
	fmt.Fprintln(s.out, s.paint(green, "✓ 订阅已修改"))
	return nil
}

func validateSubscriptionOptions(options map[string]string, edit bool) error {
	for key := range options {
		switch key {
		case "name", "auth", "user", "via", "group", "prefix":
		case "url":
			if edit {
				continue
			}
			fallthrough
		default:
			return fmt.Errorf("不支持的订阅选项 --%s；请检查拼写", key)
		}
	}
	return nil
}

func (s *Shell) subscriptionGroupCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：/sub group list|create|use|enable|disable|remove")
	}
	switch args[0] {
	case "list":
		groups, err := s.client.SubscriptionGroups(ctx)
		if err != nil {
			return err
		}
		for index, group := range groups {
			state := "DISABLED"
			if group.Enabled {
				state = "ENABLED"
			}
			fmt.Fprintf(s.out, "  %-4d %-10s %s\n", index+1, state, group.Name)
		}
		return nil
	case "create":
		if len(args) < 2 {
			return errors.New("用法：/sub group create <名称>")
		}
		if err := s.client.CreateSubscriptionGroup(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
	case "use", "enable", "disable":
		if len(args) < 2 {
			return fmt.Errorf("用法：/sub group %s <名称>", args[0])
		}
		if err := s.client.SetSubscriptionGroup(ctx, strings.Join(args[1:], " "), args[0]); err != nil {
			return err
		}
	case "remove":
		if len(args) < 2 {
			return errors.New("用法：/sub group remove <名称>")
		}
		if err := s.client.RemoveSubscriptionGroup(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
	default:
		return errors.New("用法：/sub group list|create|use|enable|disable|remove")
	}
	fmt.Fprintln(s.out, s.paint(green, "✓ 订阅分组已更新"))
	return nil
}

func (s *Shell) subscriptionByReference(ctx context.Context, reference string) (app.PublicSubscription, error) {
	items, err := s.client.Subscriptions(ctx)
	if err != nil {
		return app.PublicSubscription{}, err
	}
	if index, convErr := strconv.Atoi(reference); convErr == nil {
		if index >= 1 && index <= len(items) {
			return items[index-1], nil
		}
	}
	for _, item := range items {
		if strings.EqualFold(item.Name, reference) {
			return item, nil
		}
	}
	return app.PublicSubscription{}, fmt.Errorf("订阅 %s 不存在", reference)
}

func isHTTPURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func parseCommandOptions(args []string) ([]string, map[string]string, error) {
	positional := []string{}
	options := map[string]string{}
	for index := 0; index < len(args); index++ {
		if !strings.HasPrefix(args[index], "--") {
			positional = append(positional, args[index])
			continue
		}
		key := strings.TrimPrefix(args[index], "--")
		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
			return nil, nil, fmt.Errorf("选项 --%s 缺少值", key)
		}
		options[key] = args[index+1]
		index++
	}
	return positional, options, nil
}

func (s *Shell) nodeCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：/node list|test|use")
	}
	switch args[0] {
	case "list":
		nodes, err := s.client.Nodes(ctx)
		if err != nil {
			return err
		}
		query := strings.ToLower(strings.Join(args[1:], " "))
		return s.printNodes(nodes, query)
	case "test":
		fmt.Fprintln(s.out, s.paint(cyan, "正在并发测试节点延迟…"))
		result, err := s.client.TestNodes(ctx)
		if err != nil {
			return err
		}
		type pair struct {
			name  string
			delay int
		}
		pairs := make([]pair, 0, len(result))
		for name, delay := range result {
			pairs = append(pairs, pair{name, delay})
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].delay == pairs[j].delay {
				return pairs[i].name < pairs[j].name
			}
			if pairs[i].delay <= 0 {
				return false
			}
			if pairs[j].delay <= 0 {
				return true
			}
			return pairs[i].delay < pairs[j].delay
		})
		passed := 0
		for _, p := range pairs {
			text := s.paint(red, "未通过")
			if p.delay > 0 {
				passed++
				text = s.delay(p.delay)
			}
			fmt.Fprintf(s.out, "  %-42s %s\n", truncate(p.name, 38), text)
		}
		fmt.Fprintf(s.out, "  共 %d 个真实节点：%d 个通过，%d 个未通过（不包含 DIRECT/AUTO）。\n", len(pairs), passed, len(pairs)-passed)
		return nil
	case "use":
		if len(args) < 2 {
			nodes, err := s.client.Nodes(ctx)
			if err != nil {
				return err
			}
			s.cachedNodes = nodes
			if err := s.printNodes(nodes, ""); err != nil {
				return err
			}
			selected := s.prompt("输入节点序号或完整名称")
			if selected == "" {
				return errors.New("已取消节点选择")
			}
			args = []string{"use", selected}
		}
		name := strings.Join(args[1:], " ")
		if index, err := strconv.Atoi(name); err == nil {
			nodes := s.cachedNodes
			if len(nodes) == 0 {
				nodes, err = s.client.Nodes(ctx)
				if err != nil {
					return err
				}
			}
			if index < 1 || index > len(nodes) {
				return fmt.Errorf("节点序号必须在 1-%d 之间", len(nodes))
			}
			name = nodes[index-1].Name
		}
		if err := s.client.SelectNode(ctx, name); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 已切换到 ")+name)
		return nil
	default:
		return errors.New("用法：/node list|test|use")
	}
}

func (s *Shell) printNodes(nodes []core.Node, query string) error {
	s.cachedNodes = nodes
	if len(nodes) == 0 {
		fmt.Fprintln(s.out, s.paint(dim, "没有已加载或已缓存的节点。请执行 /sub update all --direct，并查看每个订阅的更新结果。"))
		return nil
	}
	if nodes[0].Cached {
		fmt.Fprintln(s.out, s.paint(dim, "  以下为离线缓存，非实时在线状态。选择或测速请先 /core start。"))
	}
	width := s.editorContentWidth()
	compact := width < 110
	if compact {
		fmt.Fprintln(s.out, "\n  序号 / 节点 · 下一行显示检查状态、延迟与来源")
	} else {
		fmt.Fprintln(s.out, "\n  "+nodeTableRow("序号", "节点", "协议", "订阅", "延迟", "状态"))
	}
	for index, node := range nodes {
		if query != "" && !strings.Contains(strings.ToLower(node.Name+" "+node.Type+" "+node.ProviderName), query) {
			continue
		}
		alive, statusColor := "未测速", dim
		if node.Tested || node.Delay > 0 {
			alive, statusColor = "检查未通过", red
			if node.Alive && node.Delay > 0 {
				alive, statusColor = "检查通过", green
			}
		}
		if node.Cached {
			alive, statusColor = "缓存", dim
		}
		delay := "—"
		if node.Delay > 0 {
			delay = fmt.Sprintf("%d ms", node.Delay)
		}
		if compact {
			fmt.Fprintln(s.out, truncateDisplay(fmt.Sprintf("  #%d %s", index+1, node.Name), width))
			fmt.Fprintln(s.out, s.paint(statusColor, truncateDisplay(fmt.Sprintf("     %s · %s · %s · %s", alive, delay, node.Type, node.ProviderName), width)))
		} else {
			fmt.Fprintln(s.out, "  "+nodeTableRow(strconv.Itoa(index+1), node.Name, node.Type, node.ProviderName, delay, alive))
		}
	}
	return nil
}

// nodeTableRow 按终端显示列填充，不使用按 rune 计数的 fmt 字段宽度。
// 中文与 Emoji 在控制台常占两列，否则输出自动折行后会破坏编辑器的光标定位。
func nodeTableRow(index, name, protocol, provider, delay, status string) string {
	values := []string{index, name, protocol, provider, delay, status}
	widths := []int{5, 34, 12, 14, 9, 12}
	for i, value := range values {
		text := truncateDisplay(value, widths[i])
		values[i] = text + strings.Repeat(" ", max(widths[i]-displayWidth(text), 0))
	}
	return strings.TrimRight(strings.Join(values, " "), " ")
}
func (s *Shell) modeCommand(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("用法：/mode rule|global|direct")
	}
	if err := s.client.SetMode(ctx, args[0]); err != nil {
		return err
	}
	fmt.Fprintln(s.out, s.paint(green, "✓ 已切换到 ")+strings.ToUpper(args[0])+" 模式")
	return nil
}
func (s *Shell) logs(ctx context.Context, args []string) error {
	limit := 80
	if len(args) > 0 {
		if value, err := strconv.Atoi(args[0]); err == nil && value > 0 {
			limit = value
		}
	}
	lines, err := s.client.Logs(ctx, limit)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(s.out, s.paint(dim, line))
	}
	return nil
}
func (s *Shell) doctor(ctx context.Context) error {
	items, err := s.client.Doctor(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		symbol, color := "✓", green
		if item.Status == "warning" {
			symbol, color = "!", yellow
		} else if item.Status == "error" {
			symbol, color = "×", red
		}
		fmt.Fprintf(s.out, "  %s  %-16s %s\n", s.paint(color, symbol), item.Name, s.paint(dim, item.Message))
	}
	return nil
}
func (s *Shell) web(ctx context.Context, args []string) error {
	action := "status"
	showToken := false
	if len(args) > 0 {
		action = strings.ToLower(args[0])
		showToken = action == "--show-token" || action == "token"
		if showToken {
			action = "status"
		}
	}

	switch action {
	case "start", "on":
		if err := s.startWebService(ctx); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ Web 控制服务已启动"))
	case "stop", "off":
		if err := s.stopWebService(ctx); err != nil {
			return err
		}
	case "restart":
		if err := s.stopWebService(ctx); err != nil {
			return err
		}
		if err := s.startWebService(ctx); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ Web 控制服务已重新启动"))
	case "status":
		// 状态在下方统一输出。
	default:
		return errors.New("用法：/web start|stop|restart|status|--show-token")
	}

	running, err := s.webRunning(ctx)
	if err != nil {
		return err
	}
	stateColor, stateText := yellow, "STOPPED"
	if running {
		stateColor, stateText = green, "RUNNING"
	}
	fmt.Fprintf(s.out, "  %s  %-12s %s\n", s.paint(stateColor, "●"), s.paint(bold, "Web"), s.paint(stateColor, stateText))
	fmt.Fprintln(s.out, "  控制台："+s.paint(cyan, s.webURL))
	if showToken {
		fmt.Fprintln(s.out, "  API 密钥："+s.secret)
	} else if running {
		fmt.Fprintln(s.out, s.paint(dim, "  使用 /web --show-token 显示登录密钥。请勿分享该密钥。"))
	} else {
		fmt.Fprintln(s.out, s.paint(dim, "  /web start 启动后台；停止期间仍可 /system-proxy status|recover 离线查询/恢复。"))
	}
	return nil
}

func (s *Shell) startWebService(ctx context.Context) error {
	running, err := s.webRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		return nil
	}
	if s.startWeb == nil {
		return errors.New("当前 CLI 未配置 Web 服务启动器")
	}
	return s.startWeb(ctx)
}

func (s *Shell) stopWebService(ctx context.Context) error {
	running, err := s.webRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		fmt.Fprintln(s.out, s.paint(dim, "Web 控制服务已经停止。"))
		return nil
	}
	result, shutdownErr := s.client.ShutdownWithResult(ctx)
	if shutdownErr != nil {
		return shutdownErr
	}
	if result.Warning {
		s.printProxyOperation(result)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Web 控制服务关闭超时")
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			healthy := s.client.Healthy(probeCtx)
			cancel()
			if !healthy {
				fmt.Fprintln(s.out, s.paint(green, "✓ Web 控制服务已关闭"))
				return nil
			}
		}
	}
}

// shutdown 完整关闭由 Kivo 管理的运行环境。
//
// 与 /quit 不同，本命令会先通过控制 API 停止 Mihomo。StopCore 会同步等待进程退出，
// 并关闭下次控制器启动时的内核自动启动选项；随后再关闭 Web 控制服务。只有两个步骤
// 都成功时，交互循环才会结束，避免部分失败时向用户呈现“已完全退出”的假象。
func (s *Shell) shutdown(ctx context.Context) error {
	fmt.Fprintln(s.out, s.paint(dim, "将恢复本程序管理的系统代理并停止 Core / Web；浏览器独立设置不改动。"))
	running, err := s.webRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		if s.offlineShutdown != nil {
			if err := s.offlineShutdown(); err != nil {
				return fmt.Errorf("清除 Core 自动启动偏好: %w", err)
			}
		}
		fmt.Fprintln(s.out, s.paint(dim, "Web 控制服务和其管理的代理已停止。"))
		fmt.Fprintln(s.out, s.paint(green, "✓ Kivo 已完全退出"))
		return nil
	}

	fmt.Fprintln(s.out, s.paint(cyan, "正在停止代理与 Mihomo Core…"))
	if _, err := s.client.CoreAction(ctx, "stop"); err != nil {
		return fmt.Errorf("停止代理内核: %w", err)
	}
	fmt.Fprintln(s.out, s.paint(green, "✓ 代理与 Mihomo Core 已停止"))

	if err := s.stopWebService(ctx); err != nil {
		return fmt.Errorf("关闭 Web 控制服务: %w", err)
	}
	fmt.Fprintln(s.out, s.paint(green, "✓ Kivo 已完全退出"))
	return nil
}

func (s *Shell) webRunning(ctx context.Context) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	if !s.client.Healthy(probeCtx) {
		return false, nil
	}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer verifyCancel()
	if err := s.client.Verify(verifyCtx); err != nil {
		return false, fmt.Errorf("Web 端口被其他 Kivo 实例占用: %w", err)
	}
	return true, nil
}

func (s *Shell) prompt(label string) string {
	fmt.Fprintf(s.out, "  %s: ", label)
	value, _ := s.reader.ReadString('\n')
	return strings.TrimSpace(value)
}
func (s *Shell) promptDefault(label, def string) string {
	fmt.Fprintf(s.out, "  %s [%s]: ", label, def)
	value, _ := s.reader.ReadString('\n')
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	return value
}
func (s *Shell) secretPrompt(label string) string {
	fmt.Fprintf(s.out, "  %s: ", label)
	if data, err := platform.ReadPassword(); err == nil {
		fmt.Fprintln(s.out)
		return strings.TrimSpace(string(data))
	}
	value, _ := s.reader.ReadString('\n')
	return strings.TrimSpace(value)
}
func (s *Shell) paint(code, value string) string {
	if !s.color || code == "" {
		return value
	}
	return "\x1b[" + strings.TrimPrefix(code, "\x1b[") + value + reset
}
func (s *Shell) error(err error) { fmt.Fprintln(s.out, "\n  "+s.paint(red, "× ")+err.Error()) }
func (s *Shell) delay(value int) string {
	if value <= 0 {
		return s.paint(dim, "—")
	}
	text := fmt.Sprintf("%d ms", value)
	if value < 120 {
		return s.paint(green, text)
	}
	if value < 250 {
		return s.paint(yellow, text)
	}
	return s.paint(red, text)
}
func truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max-1]) + "…"
}
func tail(values []string, index int) []string {
	if index >= len(values) {
		return nil
	}
	return values[index:]
}
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func onOff(value bool) string {
	if value {
		return "ON"
	}
	return "OFF"
}
