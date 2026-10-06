// Package controller 提供 CLI 与桌面端共用的后台生命周期，避免两套启动逻辑漂移。
package controller

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
	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core/mihomo"
	"github.com/itrunswap/Kivo/internal/platform"
	"github.com/itrunswap/Kivo/internal/server"
)

// Run 在当前进程运行后台；桌面程序的 serve 子进程不创建窗口。
func Run(store *config.Store, paths config.Paths, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "", "覆盖 Web 控制台监听地址")
	if err := flags.Parse(args); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(paths.LogDir, "controller.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开控制器日志: %w", err)
	}
	defer logFile.Close()
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	manager := mihomo.NewManager(store)
	service := app.NewService(store, manager)
	host := server.New(service, store, logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	serverErr := make(chan error, 1)
	go func() { serverErr <- host.Run(ctx, *listen) }()
	select {
	case <-host.Ready():
		manager.SetProviderListen(host.ListenAddress())
	case err = <-serverErr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	preferences := store.Snapshot()
	if preferences.Mihomo.AutoStart || preferences.SystemProxy.AutoConnect || preferences.SystemProxy.Lease != nil {
		go func() {
			startCtx, done := context.WithTimeout(ctx, 60*time.Second)
			defer done()
			if err := service.Resume(startCtx); err != nil {
				logger.Error("自动恢复代理失败", "error", err)
			}
		}()
	}
	err = <-serverErr
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	// 退出服务只恢复本程序接管的系统代理，不改变下次启动偏好。
	stopErr := service.SafeShutdown(stopCtx)
	if stopErr != nil {
		logger.Error("退出时恢复代理失败，备份保留，请运行 system-proxy recover", "error", stopErr)
	}
	return errors.Join(err, stopErr)
}

// Ensure 复用已认证的本地后台，或启动当前可执行文件的隐藏 serve 子进程。
func Ensure(ctx context.Context, api *client.Client, paths config.Paths) error {
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
	if err := platform.StartDetached(executable, []string{"serve", "--data-dir", paths.Root}, filepath.Join(paths.LogDir, "daemon.log")); err != nil {
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

// ExtractDataDir 允许 --data-dir 出现在子命令前后，保持 CLI 与桌面端一致。
func ExtractDataDir(args []string) (string, []string, error) {
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
