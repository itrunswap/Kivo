// Kivo Desktop 使用系统 WebView，所有代理业务仍由共用后台负责。
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/controller"
	"github.com/itrunswap/Kivo/internal/core"
	bridge "github.com/itrunswap/Kivo/internal/desktop"
	"github.com/itrunswap/Kivo/internal/server"
	"github.com/itrunswap/Kivo/internal/version"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend/*
var frontend embed.FS

//go:embed build/appicon.png
var appIcon []byte

// desktopAssets 只复用公共字体，避免缺失文件意外回退成完整 Web 管理页。
type desktopAssets struct{ primary, shared fs.FS }

func (a desktopAssets) Open(name string) (fs.File, error) {
	if strings.HasPrefix(name, "fonts/") {
		return a.shared.Open(name)
	}
	return a.primary.Open(name)
}

// Desktop 只绑定有限的原生能力，不向 JavaScript 暴露配置 Store 或凭据。
type Desktop struct {
	ctx          context.Context
	cancel       context.CancelFunc
	bridge       *bridge.Bridge
	mu           sync.RWMutex
	starting     bool
	ready        bool
	startupError string
	inflight     int
	installMu    sync.Mutex
	quitMu       sync.Mutex
	smoke        bool
	smokeOnce    sync.Once
}

// DesktopInfo 包含可恢复的后台启动状态。
type DesktopInfo struct {
	bridge.Info
	Ready     bool   `json:"ready"`
	Starting  bool   `json:"starting"`
	Error     string `json:"error,omitempty"`
	SmokeTest bool   `json:"smokeTest"`
}

func (d *Desktop) startup(ctx context.Context) {
	d.mu.Lock()
	d.ctx, d.cancel = context.WithCancel(ctx)
	ownContext := d.ctx
	d.mu.Unlock()
	go d.Reconnect()
	if d.smoke {
		go func() {
			select {
			case <-ownContext.Done():
				return
			case <-time.After(45 * time.Second):
				d.NativeReady(`{"error":"原生页面未在 45 秒内完成初始化"}`)
			}
		}()
	}
}

func (d *Desktop) shutdown(context.Context) {
	d.mu.RLock()
	cancel := d.cancel
	d.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// showWindow 防止第二实例在窗口尚未完成启动时使用空的框架上下文。
func (d *Desktop) showWindow(options.SecondInstanceData) {
	d.mu.RLock()
	ctx := d.ctx
	d.mu.RUnlock()
	if ctx != nil {
		wruntime.WindowUnminimise(ctx)
		wruntime.WindowShow(ctx)
	}
}

// closing 防止窗口关闭截断安装或系统代理事务；关闭窗口从不偷偷停止后台。
func (d *Desktop) closing(ctx context.Context) bool {
	d.mu.RLock()
	busy := d.inflight > 0
	d.mu.RUnlock()
	if busy {
		_, _ = wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{Type: wruntime.InfoDialog, Title: "操作正在进行", Message: "请等待安装或代理操作完成后再关闭窗口。", Buttons: []string{"知道了"}})
	}
	return busy
}

func (d *Desktop) beginOperation() func() {
	d.mu.Lock()
	d.inflight++
	d.mu.Unlock()
	return func() { d.mu.Lock(); d.inflight--; d.mu.Unlock() }
}

// Reconnect 失败时保留窗口和错误提示，用户可修复配置后重试。
func (d *Desktop) Reconnect() string {
	d.mu.Lock()
	if d.starting {
		d.mu.Unlock()
		return "后台正在启动"
	}
	d.starting = true
	d.ready = false
	d.startupError = ""
	d.mu.Unlock()
	store, err := config.Load(d.bridge.Paths)
	if err == nil {
		var address string
		address, err = bridge.LocalURL(store.Snapshot().Web.Listen)
		if err == nil {
			ctx, done := context.WithTimeout(d.ctx, 12*time.Second)
			err = controller.Ensure(ctx, client.New(strings.TrimPrefix(address, "http://"), store.Snapshot().Web.Secret), d.bridge.Paths)
			done()
		}
	}
	d.mu.Lock()
	d.starting = false
	d.ready = err == nil
	if err != nil {
		d.startupError = err.Error()
	}
	message := d.startupError
	d.mu.Unlock()
	return message
}

// Info 的后台状态不等同于代理状态；代理是否接入由 overview 判断。
func (d *Desktop) Info() DesktopInfo {
	info, err := d.bridge.Info()
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := DesktopInfo{Info: info, Ready: d.ready, Starting: d.starting, Error: d.startupError, SmokeTest: d.smoke}
	if err != nil {
		result.Error = err.Error()
		result.Ready = false
	}
	return result
}

// Request 将原生调用转发到受限的本机 API。
func (d *Desktop) Request(method, path, body string) bridge.Reply {
	if method != "GET" {
		done := d.beginOperation()
		defer done()
	}
	return d.bridge.Request(d.ctx, method, path, body)
}

// InstallCore 通过事件发送真实下载字节数和速度，不阻塞 UI 线程。
func (d *Desktop) InstallCore(target string) string {
	if !d.installMu.TryLock() {
		return "内核安装正在进行"
	}
	defer d.installMu.Unlock()
	done := d.beginOperation()
	defer done()
	if err := d.bridge.Install(d.ctx, target, func(event core.InstallEvent) { wruntime.EventsEmit(d.ctx, "core:progress", event) }); err != nil {
		return err.Error()
	}
	return ""
}

// ImportCore 使用原生文件选择器，取消选择不产生任何操作。
func (d *Desktop) ImportCore() bridge.Reply {
	done := d.beginOperation()
	defer done()
	path, err := wruntime.OpenFileDialog(d.ctx, wruntime.OpenDialogOptions{Title: "选择 Mihomo 官方安装包或可执行文件"})
	if err != nil {
		return bridge.Reply{Error: "无法打开文件选择器"}
	}
	if path == "" {
		return bridge.Reply{Status: 204}
	}
	body, _ := json.Marshal(map[string]string{"path": path})
	return d.bridge.Request(d.ctx, "POST", "/api/v1/core/import", string(body))
}

// OpenWeb 不把 Token 拼接到 URL；Web 使用自己的鉴权流程。
func (d *Desktop) OpenWeb() string {
	info, err := d.bridge.Info()
	if err != nil {
		return err.Error()
	}
	wruntime.BrowserOpenURL(d.ctx, info.WebURL)
	return ""
}

// SetTheme 同步 Windows 原生标题栏与页面；其余平台保留系统原生行为。
func (d *Desktop) SetTheme(theme string) {
	switch theme {
	case "light":
		wruntime.WindowSetLightTheme(d.ctx)
	case "dark":
		wruntime.WindowSetDarkTheme(d.ctx)
	default:
		wruntime.WindowSetSystemDefaultTheme(d.ctx)
	}
}

// Quit 不强制覆盖其他程序的代理设置；恢复失败时保留窗口让用户处理。
func (d *Desktop) Quit(disconnect bool) string {
	d.mu.RLock()
	busy := d.inflight > 0
	d.mu.RUnlock()
	if busy {
		return "操作正在进行，请等待完成后再退出"
	}
	if !d.quitMu.TryLock() {
		return "正在处理退出"
	}
	defer d.quitMu.Unlock()
	if disconnect {
		result := d.bridge.Request(d.ctx, "POST", "/api/v1/connection/disconnect", "{}")
		if result.Error != "" {
			return result.Error
		}
		var operation struct {
			Warning bool   `json:"warning"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(result.Data, &operation); err != nil {
			return "无法确认代理恢复结果，暂不退出"
		}
		if operation.Warning {
			return operation.Message
		}
	}
	wruntime.Quit(d.ctx)
	return ""
}

// NativeReady 仅在显式指定隔离数据目录的烟雾测试模式写固定报告，正常运行无效果。
func (d *Desktop) NativeReady(report string) {
	if !d.smoke || len(report) > 64<<10 || !json.Valid([]byte(report)) {
		return
	}
	d.smokeOnce.Do(func() {
		_ = os.WriteFile(filepath.Join(d.bridge.Paths.Root, "desktop-smoke.json"), []byte(report), 0o600)
		wruntime.Quit(d.ctx)
	})
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Kivo Desktop:", err)
		showStartupError(err.Error())
		os.Exit(1)
	}
}

func run(rawArgs []string) error {
	dataDir, args, err := controller.ExtractDataDir(rawArgs)
	if err != nil {
		return err
	}
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Printf("Kivo Desktop %s (%s)\n", version.Version, version.Commit)
		return nil
	}
	smoke := len(args) == 1 && args[0] == "--smoke-test"
	if smoke && dataDir == "" {
		return errors.New("烟雾测试必须显式指定 --data-dir 隔离目录")
	}
	if len(args) > 0 && args[0] != "serve" && !smoke {
		return errors.New("桌面端参数：--data-dir <目录>、version、serve、--smoke-test")
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
		return controller.Run(store, paths, args[1:])
	}
	if smoke {
		cfg := store.Snapshot()
		if cfg.SystemProxy.AutoConnect || cfg.SystemProxy.Lease != nil || cfg.Mihomo.AutoStart {
			return errors.New("烟雾测试配置必须关闭自动代理和自动内核启动，且不得存在代理备份")
		}
	}
	ui, err := fs.Sub(frontend, "frontend")
	if err != nil {
		return err
	}
	app := &Desktop{bridge: bridge.New(paths), smoke: smoke}
	identityPath := filepath.Clean(paths.Root)
	if runtime.GOOS == "windows" {
		identityPath = strings.ToLower(identityPath)
	}
	identity := sha256.Sum256([]byte(identityPath))
	return wails.Run(&options.App{
		Title: "Kivo", Width: 480, Height: 760, MinWidth: 400, MinHeight: 600,
		BackgroundColour: options.NewRGB(250, 250, 250),
		AssetServer:      &assetserver.Options{Assets: desktopAssets{primary: ui, shared: server.EmbeddedAssets()}},
		OnStartup:        app.startup, OnShutdown: app.shutdown, OnBeforeClose: app.closing, Bind: []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "kivo-desktop-" + hex.EncodeToString(identity[:16]), OnSecondInstanceLaunch: app.showWindow},
		Windows:            &windows.Options{Theme: windows.SystemDefault, DisablePinchZoom: true, WebviewUserDataPath: filepath.Join(paths.Root, "desktop-webview"), ResizeDebounceMS: 50},
		Mac:                &mac.Options{DisableZoom: true, About: &mac.AboutInfo{Title: "Kivo", Message: "轻量级代理控制客户端", Icon: appIcon}},
		Linux:              &linux.Options{ProgramName: "kivo-desktop", Icon: appIcon, WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand},
	})
}
