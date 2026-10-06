package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/itrunswap/Kivo/internal/config"
)

// routeCommand 管理路由配置、可复用规则组及组内有序规则。
func (s *Shell) routeCommand(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return errors.New("用法：/route profile list|use|create，/route group list|create，/route rule list|add|remove")
	}
	switch args[0] {
	case "profile":
		return s.routeProfileCommand(ctx, args[1:])
	case "group":
		return s.routeGroupCommand(ctx, args[1:])
	case "rule":
		return s.routeRuleCommand(ctx, args[1:])
	default:
		return errors.New("用法：/route profile|group|rule")
	}
}

func (s *Shell) routeProfileCommand(ctx context.Context, args []string) error {
	switch args[0] {
	case "list":
		routing, err := s.client.Routing(ctx)
		if err != nil {
			return err
		}
		for index, profile := range routing.Profiles {
			marker := " "
			if strings.EqualFold(profile.Name, routing.ActiveProfile) {
				marker = "*"
			}
			fmt.Fprintf(s.out, "  %s %-4d %-18s 默认=%-7s 规则组=%s\n", marker, index+1, profile.Name, profile.DefaultAction, strings.Join(profile.Groups, ", "))
		}
		return nil
	case "use":
		if len(args) < 2 {
			return errors.New("用法：/route profile use <名称>")
		}
		if err := s.client.UseRouteProfile(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 路由配置已切换并应用"))
		return nil
	case "create":
		positional, options, err := parseCommandOptions(args[1:])
		if err != nil {
			return err
		}
		if len(positional) == 0 {
			return errors.New("用法：/route profile create <名称> --default proxy|direct|reject [--groups 组1,组2]")
		}
		defaultAction := options["default"]
		if defaultAction == "" {
			defaultAction = "proxy"
		}
		groups := []string{}
		if value := options["groups"]; value != "" {
			for _, group := range strings.Split(value, ",") {
				if group = strings.TrimSpace(group); group != "" {
					groups = append(groups, group)
				}
			}
		}
		if err := s.client.CreateRouteProfile(ctx, config.RouteProfile{Name: strings.Join(positional, " "), DefaultAction: defaultAction, Groups: groups}); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 路由配置已创建"))
		return nil
	case "attach", "detach":
		if len(args) != 3 {
			return fmt.Errorf("用法：/route profile %s <配置> <规则组>", args[0])
		}
		if err := s.client.SetRouteProfileGroup(ctx, args[1], args[2], args[0] == "attach"); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 路由配置的规则组已更新"))
		return nil
	case "remove":
		if len(args) < 2 {
			return errors.New("用法：/route profile remove <名称>")
		}
		if err := s.client.DeleteRouteProfile(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 路由配置已删除"))
		return nil
	default:
		return errors.New("用法：/route profile list|use|create|attach|detach|remove")
	}
}

func (s *Shell) routeGroupCommand(ctx context.Context, args []string) error {
	switch args[0] {
	case "list":
		routing, err := s.client.Routing(ctx)
		if err != nil {
			return err
		}
		for index, group := range routing.RuleGroups {
			fmt.Fprintf(s.out, "  %-4d %-22s %d 条规则\n", index+1, group.Name, len(group.Rules))
		}
		return nil
	case "create":
		if len(args) < 2 {
			return errors.New("用法：/route group create <名称>")
		}
		if err := s.client.CreateRuleGroup(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 规则组已创建"))
		return nil
	case "remove":
		if len(args) < 2 {
			return errors.New("用法：/route group remove <名称>")
		}
		if err := s.client.DeleteRuleGroup(ctx, strings.Join(args[1:], " ")); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 规则组已删除"))
		return nil
	default:
		return errors.New("用法：/route group list|create|remove")
	}
}

func (s *Shell) routeRuleCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：/route rule list|add|remove")
	}
	switch args[0] {
	case "list":
		if len(args) < 2 {
			return errors.New("用法：/route rule list <规则组>")
		}
		name := strings.Join(args[1:], " ")
		routing, err := s.client.Routing(ctx)
		if err != nil {
			return err
		}
		for _, group := range routing.RuleGroups {
			if !strings.EqualFold(group.Name, name) {
				continue
			}
			for index, rule := range group.Rules {
				fmt.Fprintf(s.out, "  %-4d %-16s %-28s %s\n", index+1, rule.Type, rule.Value, strings.ToUpper(rule.Action))
			}
			return nil
		}
		return fmt.Errorf("规则组 %s 不存在", name)
	case "add":
		if len(args) < 5 {
			return errors.New("用法：/route rule add <规则组> <proxy|direct|reject> <类型> <值>")
		}
		rule := config.RouteRule{Action: args[2], Type: args[3], Value: strings.Join(args[4:], " ")}
		if err := s.client.AddRouteRule(ctx, args[1], rule); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 路由规则已添加并应用"))
		return nil
	case "remove":
		if len(args) != 3 {
			return errors.New("用法：/route rule remove <规则组> <序号>")
		}
		index, err := strconv.Atoi(args[2])
		if err != nil {
			return errors.New("规则序号必须是数字")
		}
		if err := s.client.RemoveRouteRule(ctx, args[1], index); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 路由规则已删除并应用"))
		return nil
	default:
		return errors.New("用法：/route rule list|add|remove")
	}
}

func (s *Shell) portCommand(ctx context.Context, args []string) error {
	settings, err := s.client.Settings(ctx)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		fmt.Fprintf(s.out, "  HTTP + SOCKS5 混合代理：127.0.0.1:%d\n", settings.MixedPort)
		return nil
	}
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		return errors.New("端口必须是 1-65535 之间的数字")
	}
	settings.MixedPort = port
	if err := s.client.UpdateSettings(ctx, settings); err != nil {
		return err
	}
	fmt.Fprintf(s.out, "%s 代理端口已切换为 127.0.0.1:%d\n", s.paint(green, "✓"), port)
	return nil
}

func (s *Shell) toggleSetting(ctx context.Context, target string, args []string) error {
	settings, err := s.client.Settings(ctx)
	if err != nil {
		return err
	}
	current := settings.TUNEnabled
	if target == "lan" {
		current = settings.AllowLAN
	}
	if len(args) == 0 || args[0] == "status" {
		fmt.Fprintf(s.out, "  %s：%s\n", strings.ToUpper(target), onOff(current))
		return nil
	}
	var next bool
	switch strings.ToLower(args[0]) {
	case "on", "enable":
		next = true
	case "off", "disable":
		next = false
	default:
		return fmt.Errorf("用法：/%s status|on|off", target)
	}
	if target == "lan" {
		settings.AllowLAN = next
	} else {
		settings.TUNEnabled = next
	}
	if err := s.client.UpdateSettings(ctx, settings); err != nil {
		return err
	}
	fmt.Fprintf(s.out, "%s %s 已%s\n", s.paint(green, "✓"), strings.ToUpper(target), map[bool]string{true: "开启", false: "关闭"}[next])
	return nil
}

func (s *Shell) configCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "show" {
		settings, err := s.client.Settings(ctx)
		if err != nil {
			return err
		}
		routing, err := s.client.Routing(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(s.out, "  Web：%s\n  代理端口：%d\n  模式：%s\n  LAN：%s\n  TUN：%s\n  路由配置：%s\n  下载代理：%s\n  下载重试：%d\n", settings.Listen, settings.MixedPort, settings.Mode, onOff(settings.AllowLAN), onOff(settings.TUNEnabled), routing.ActiveProfile, valueOr(settings.DownloadProxy, "SYSTEM"), settings.DownloadRetry)
		return nil
	}
	if args[0] == "download-proxy" {
		settings, err := s.client.Settings(ctx)
		if err != nil {
			return err
		}
		if len(args) == 1 {
			fmt.Fprintln(s.out, valueOr(settings.DownloadProxy, "SYSTEM"))
			return nil
		}
		value := strings.Join(args[1:], " ")
		if value == "off" || value == "system" {
			value = ""
		}
		settings.DownloadProxy = value
		if err := s.client.UpdateSettings(ctx, settings); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 内核下载代理已更新"))
		return nil
	}
	if args[0] == "download-retry" {
		if len(args) != 2 {
			return errors.New("用法：/config download-retry <1-10>")
		}
		count, err := strconv.Atoi(args[1])
		if err != nil || count < 1 || count > 10 {
			return errors.New("重试次数必须在 1-10 之间")
		}
		settings, err := s.client.Settings(ctx)
		if err != nil {
			return err
		}
		settings.DownloadRetry = count
		if err := s.client.UpdateSettings(ctx, settings); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.paint(green, "✓ 下载重试次数已更新"))
		return nil
	}
	if args[0] == "validate" {
		items, err := s.client.Doctor(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			fmt.Fprintf(s.out, "  %-16s %-8s %s\n", item.Name, strings.ToUpper(item.Status), item.Message)
		}
		return nil
	}
	return errors.New("用法：/config show|validate|download-proxy|download-retry")
}
