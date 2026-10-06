package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/itrunswap/Kivo/internal/app"
)

func parseConnectOptions(args []string, allowCheck bool) (app.ConnectOptions, error) {
	var options app.ConnectOptions
	for _, arg := range args {
		switch arg {
		case "--replace":
			options.Replace = true
		case "--adopt":
			options.Adopt = true
		case "--no-check":
			if !allowCheck {
				return options, errors.New("--no-check 只用于 /connect")
			}
			options.SkipCheck = true
		default:
			return options, fmt.Errorf("不支持的选项 %q；支持 --replace、--adopt，/connect 可加 --no-check", arg)
		}
	}
	if options.Adopt && options.Replace {
		return options, errors.New("--adopt 与 --replace 不能同时使用")
	}
	return options, nil
}

func (s *Shell) connectionCommand(ctx context.Context, action string, args []string) error {
	if action == "disconnect" && len(args) > 0 {
		return errors.New("用法：/disconnect（恢复受管系统代理并停止内核）")
	}
	options, err := parseConnectOptions(args, true)
	if err != nil {
		return err
	}
	var result app.ProxyOperationResult
	if action == "connect" {
		fmt.Fprintln(s.out, s.paint(cyan, "正在连接：启动内核、接入系统代理"))
		if !options.SkipCheck {
			fmt.Fprintln(s.out, s.paint(dim, "连接后检测外网；不会覆盖其他代理，除非显式 --replace。"))
		}
		result, err = s.client.Connect(ctx, options)
	} else {
		running, probeErr := s.webRunning(ctx)
		if probeErr != nil {
			return probeErr
		}
		if !running && s.offlineSystemProxy != nil {
			result, err = s.offlineSystemProxy(ctx, "disconnect", false)
		} else {
			result, err = s.client.Disconnect(ctx)
		}
	}
	s.printProxyOperation(result)
	return err
}

func (s *Shell) systemProxyCommand(ctx context.Context, args []string) error {
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	if action != "status" && action != "on" && action != "off" && action != "recover" {
		return errors.New("用法：/system-proxy status|on|off|recover；on 可加 --replace/--adopt，recover 可加 --force")
	}
	force := false
	var options app.ConnectOptions
	var err error
	if action == "on" {
		options, err = parseConnectOptions(args, false)
	} else if action == "recover" && len(args) == 1 && args[0] == "--force" {
		force = true
	} else if len(args) > 0 {
		err = errors.New("status/off 不接受选项；recover 仅接受 --force")
	}
	if err != nil {
		return err
	}
	running, err := s.webRunning(ctx)
	if err != nil {
		return err
	}
	if !running && action != "on" && s.offlineSystemProxy != nil {
		result, err := s.offlineSystemProxy(ctx, action, force)
		s.printProxyOperation(result)
		return err
	}
	if action == "status" {
		status, err := s.client.SystemProxyStatus(ctx)
		if err != nil {
			return err
		}
		s.printProxyOperation(app.ProxyOperationResult{Message: status.Message, SystemProxy: status, Warning: status.RecoveryPending || status.State == "unknown" || status.State == "other" || status.State == "automatic"})
		return nil
	}
	if force {
		fmt.Fprintln(s.out, s.paint(yellow, "显式强制恢复：将覆盖备份范围内的外部修改；不改变其他设置。"))
	}
	result, err := s.client.SystemProxyAction(ctx, action, options, force)
	s.printProxyOperation(result)
	return err
}

func (s *Shell) printProxyOperation(result app.ProxyOperationResult) {
	if result.Message != "" {
		style, marker := green, "✓ "
		if result.Warning {
			style, marker = yellow, "! "
		}
		fmt.Fprintln(s.out, s.paint(style, marker+result.Message))
	}
	if result.SystemProxy.Message != "" && result.SystemProxy.Message != result.Message {
		fmt.Fprintln(s.out, "  "+result.SystemProxy.Message)
	}
	if result.Connectivity != nil {
		s.printConnectivity(*result.Connectivity)
	}
	if result.SystemProxy.RecoveryPending {
		fmt.Fprintln(s.out, s.paint(yellow, "  备份保留 · /system-proxy recover；--force 会覆盖外部修改，请谨慎。"))
	}
}
