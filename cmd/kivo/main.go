package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/cli"
	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/controller"
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
	return controller.Run(store, paths, args)
}

func ensureDaemon(ctx context.Context, api *client.Client, paths config.Paths) error {
	return controller.Ensure(ctx, api, paths)
}

func extractDataDir(args []string) (string, []string, error) {
	return controller.ExtractDataDir(args)
}
