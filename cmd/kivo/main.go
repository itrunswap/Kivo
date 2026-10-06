package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/cli"
	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core/mihomo"
	"github.com/itrunswap/Kivo/internal/platform"
	"github.com/itrunswap/Kivo/internal/server"
	"github.com/itrunswap/Kivo/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Kivo:", err)
		os.Exit(1)
	}
}

func run(rawArgs []string) error {
	dataDir, args, err := extractDataDir(rawArgs)
	if err != nil {
		return err
	}
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("Kivo %s (%s, %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}
	paths, err := config.ResolvePaths(dataDir)
	if err != nil {
		return err
	}
	store, err := config.Load(paths)
	if err != nil {
		return err
	}

	if len(args) > 0 && args[0] == "serve" {
		return runServer(store, paths, args[1:])
	}

	cfg := store.Snapshot()
	api := client.New(cfg.Web.Listen, cfg.Web.Secret)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	webURL := "http://" + cfg.Web.Listen
	shell := cli.NewShell(api, os.Stdin, os.Stdout, webURL, cfg.Web.Secret)
	shell.SetConnectionRefresher(func() (*client.Client, string, string, error) {
		fresh, err := config.Load(paths)
		if err != nil {
			return nil, "", "", err
		}
		next := fresh.Snapshot()
		api = client.New(next.Web.Listen, next.Web.Secret)
		return api, "http://" + next.Web.Listen, next.Web.Secret, nil
	})
	shell.SetWebStarter(func(startCtx context.Context) error { return ensureDaemon(startCtx, api, paths) })
	shell.SetOfflineSystemProxy(func(actionCtx context.Context, action string, force bool) (app.ProxyOperationResult, error) {
		fresh, err := config.Load(paths)
		if err != nil {
			return app.ProxyOperationResult{}, err
		}
		service := app.NewService(fresh, nil)
		if action == "status" {
			status := service.SystemProxyStatus(actionCtx)
			return app.ProxyOperationResult{Message: status.Message, SystemProxy: status, Warning: status.RecoveryPending || status.State == "unknown" || status.State == "other" || status.State == "automatic"}, nil
		}
		result, err := service.RestoreSystemProxy(actionCtx, force)
		if err == nil && action == "disconnect" {
			err = fresh.Update(func(next *config.Config) error { next.Mihomo.AutoStart = false; return nil })
			result.Message += "；后台未运行，已取消下次自动启动（不会终止其他程序的内核）"
		}
		return result, err
	})
	shell.SetOfflineShutdown(func() error {
		fresh, err := config.Load(paths)
		if err != nil {
			return err
		}
		recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer recoverCancel()
		result, err := app.NewService(fresh, nil).RestoreSystemProxy(recoverCtx, false)
		if err != nil {
			return err
		}
		if result.Warning {
			fmt.Fprintln(os.Stdout, result.Message)
		}
		return fresh.Update(func(next *config.Config) error {
			next.Mihomo.AutoStart = false
			next.SystemProxy.AutoConnect = false
			return nil
		})
	})

	// Web 生命周期命令不得先自动启动服务，否则 status/stop 会改变被查询的状态。
	commandName := ""
	if len(args) > 0 {
		commandName = strings.TrimPrefix(args[0], "/")
	}
	webLifecycle := commandName == "web" || commandName == "shutdown" || commandName == "daemon" || commandName == "disconnect" || (commandName == "system-proxy" && (len(args) == 1 || args[1] != "on"))
	if !webLifecycle {
		if err := ensureDaemon(ctx, api, paths); err != nil {
			return err
		}
	}
	if len(args) > 0 && args[0] == "daemon" {
		if len(args) != 2 || args[1] != "stop" {
			return errors.New("用法：kivo daemon stop")
		}
		args = []string{"web", "stop"}
	}

	if len(args) == 0 {
		return shell.Run(ctx)
	}
	if strings.HasPrefix(args[0], "/") {
		args[0] = strings.TrimPrefix(args[0], "/")
	}
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cmdCancel()
	return shell.Execute(cmdCtx, args)
}

func runServer(store *config.Store, paths config.Paths, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "", "覆盖 Web 控制台监听地址")
	if err := flags.Parse(args); err != nil {
		return err
	}

	logPath := filepath.Join(paths.LogDir, "controller.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开控制器日志: %w", err)
	}
	defer logFile.Close()
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))

	manager := mihomo.NewManager(store)
	service := app.NewService(store, manager)
	controller := server.New(service, store, logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() { serverErr <- controller.Run(ctx, *listen) }()
	select {
	case <-controller.Ready():
		// AES provider 依赖本机控制端点，因此必须先建立 HTTP 监听器，再恢复内核。
		manager.SetProviderListen(controller.ListenAddress())
	case err = <-serverErr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	// 已安装内核时尝试恢复上次运行。启动失败不会阻塞控制台，用户仍可查看日志和修复配置。
	preferences := store.Snapshot()
	if preferences.Mihomo.AutoStart || preferences.SystemProxy.AutoConnect || preferences.SystemProxy.Lease != nil {
		go func() {
			startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
			defer startCancel()
			if startErr := service.Resume(startCtx); startErr != nil {
				logger.Error("自动恢复代理失败", "error", startErr)
			}
		}()
	}
	err = <-serverErr
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	// 控制器自身退出不应改变用户的自动启动偏好，只终止当前子进程。
	stopErr := service.SafeShutdown(stopCtx)
	if stopErr != nil {
		logger.Error("退出时恢复代理失败，备份保留，请运行 system-proxy recover", "error", stopErr)
	}
	return errors.Join(err, stopErr)
}

func ensureDaemon(ctx context.Context, api *client.Client, paths config.Paths) error {
	probeCtx, probeCancel := context.WithTimeout(ctx, 800*time.Millisecond)
	healthy := api.Healthy(probeCtx)
	probeCancel()
	if healthy {
		verifyCtx, verifyCancel := context.WithTimeout(ctx, 800*time.Millisecond)
		err := api.Verify(verifyCtx)
		verifyCancel()
		if err != nil {
			return fmt.Errorf("控制端口已被其他 Kivo 实例占用，请停止该实例或修改当前数据目录中的 web.listen: %w", err)
		}
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位当前程序: %w", err)
	}
	args := []string{"serve", "--data-dir", paths.Root}
	if err := platform.StartDetached(executable, args, filepath.Join(paths.LogDir, "daemon.log")); err != nil {
		return err
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("后台服务启动超时，请检查 %s", filepath.Join(paths.LogDir, "controller.log"))
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := api.Verify(checkCtx)
			cancel()
			if err == nil {
				return nil
			}
		}
	}
}

func extractDataDir(args []string) (string, []string, error) {
	result := make([]string, 0, len(args))
	dataDir := ""
	for index := 0; index < len(args); index++ {
		if args[index] == "--data-dir" {
			if index+1 >= len(args) {
				return "", nil, errors.New("--data-dir 缺少路径")
			}
			dataDir = args[index+1]
			index++
			continue
		}
		if strings.HasPrefix(args[index], "--data-dir=") {
			dataDir = strings.TrimPrefix(args[index], "--data-dir=")
			continue
		}
		result = append(result, args[index])
	}
	return dataDir, result, nil
}
