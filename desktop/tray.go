package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/itrunswap/Kivo/desktop/internal/tray"
	"github.com/itrunswap/Kivo/internal/app"
	"github.com/wailsapp/wails/v2/pkg/options"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// trayView 是纯状态派生，不根据按钮偏好猜测已连接；检测结果过期后立即降级。
func trayView(o app.Overview, online, busy bool, now time.Time) tray.View {
	v := tray.View{Title: "后台未连接", Node: "未确认", Tone: "error", Busy: busy}
	if !online {
		v.Tip = "Kivo · 后台未连接（状态未知）"
		return v
	}
	running := o.Core.State == "running"
	attached := o.SystemProxy.State == "this_app"
	on := running && attached && o.ProxyPortListening
	v.Title = "未连接"
	v.Tone = "idle"
	v.Node = tray.Clean(o.Core.CurrentNode, 64)
	switch {
	case attached && !on:
		v.Title = "代理入口异常"
		v.Tone = "error"
	case on:
		v.Title = "已连接 · 未检测"
		v.Tone = "active"
	case running:
		v.Title = "仅内核运行"
	case o.Core.State == "not_installed":
		v.Title = "内核未安装"
	}
	if o.SystemProxy.State == "unknown" {
		v.Title = "系统接入未确认"
		v.Tone = "error"
	}
	if report := o.Connectivity; on && report != nil && !report.Stale && !report.CheckedAt.IsZero() && report.CheckedAt.After(now.Add(-2*time.Minute)) && !report.CheckedAt.After(now.Add(5*time.Second)) {
		for _, route := range report.Routes {
			if route.ID == "entry" {
				switch route.State {
				case "ok":
					v.Title = "已连接 · 检测通过"
					v.Tone = "good"
				case "failed", "partial":
					v.Title = "已接入 · 联网异常"
					v.Tone = "error"
				}
			}
		}
	}
	if on && o.Core.Mode == "direct" {
		v.Title = "已接入 · 直连模式"
	}
	if o.SystemProxy.RecoveryPending {
		v.Title += " · 待恢复备份"
	}
	if o.TUNEnabled && !attached {
		v.Title += " · TUN 已配置"
	} // 配置开启不能当作实际接管或检测通过。
	v.Connect = o.SystemProxy.Supported && o.SystemProxy.State != "unknown" && !attached && !o.SystemProxy.RecoveryPending && o.Core.State != "not_installed"
	v.Disconnect = running || attached || o.SystemProxy.Managed || o.SystemProxy.RecoveryPending
	v.Check = running && o.ProxyPortListening
	v.Tip = "Kivo · " + v.Title + "\n节点：" + v.Node
	if busy {
		v.Tip = "Kivo · 操作进行中\n" + v.Title
	}
	return v
}

func (d *Desktop) startTray(ctx context.Context) {
	initCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	driver, err := tray.Start(initCtx, tray.Callbacks{Action: d.queueTrayAction, Availability: d.trayAvailability})
	cancel()
	d.mu.Lock()
	d.trayStarting = false
	if err != nil {
		d.trayError = "托盘不可用，保持普通窗口模式：" + err.Error()
		d.trayReady = false
	} else {
		d.tray = driver
		d.trayReady = true
	}
	d.mu.Unlock()
	if err != nil {
		return
	}
	defer driver.Close()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		d.refreshTray(ctx, driver)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.trayWake:
		}
	}
}

func (d *Desktop) trayAvailability(ready bool, reason string) {
	d.mu.Lock()
	wasHidden := d.hidden
	changed := d.trayReady != ready
	d.trayReady = ready
	if ready {
		d.trayError = ""
	} else {
		d.trayError = "托盘宿主不可用，主窗口已保留或恢复。"
	}
	d.mu.Unlock()
	// 回调可能在原生消息线程，不能等待网络或调用 Close 造成重入。
	if !ready && wasHidden && changed {
		d.showWindow(options.SecondInstanceData{})
	}
}

func (d *Desktop) wakeTray() {
	if d.trayWake != nil {
		select {
		case d.trayWake <- struct{}{}:
		default:
		}
	}
}

func (d *Desktop) refreshTray(ctx context.Context, driver tray.Driver) {
	readCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	result := d.bridge.Request(readCtx, "GET", "/api/v1/overview", "")
	cancel()
	var overview app.Overview
	online := result.Error == "" && result.Status == 200 && json.Unmarshal(result.Data, &overview) == nil
	online = online && overview.Core.State != "" && overview.SystemProxy.State != ""
	d.mu.Lock()
	v := trayView(overview, online, d.inflight > 0 || d.quitting, time.Now())
	d.trayView = v
	d.mu.Unlock()
	driver.Update(v)
}

// HideWindow 仅在托盘注册成功且没有未完成事务时隐藏，避免窗口消失后无法找回。
func (d *Desktop) HideWindow() string {
	d.windowMu.Lock()
	defer d.windowMu.Unlock()
	d.mu.Lock()
	if !d.trayReady || d.tray == nil {
		d.mu.Unlock()
		return "托盘尚未可用，不能隐藏窗口"
	}
	if d.inflight > 0 || d.quitting || d.allowExit || d.ctx == nil {
		d.mu.Unlock()
		return "操作正在进行，请完成后再隐藏"
	}
	d.hidden = true
	d.mu.Unlock()
	wruntime.WindowHide(d.ctx)
	return ""
}

func (d *Desktop) queueTrayAction(action tray.Action) {
	if action == tray.Show {
		d.showWindow(options.SecondInstanceData{})
		return
	}
	select {
	case d.trayActions <- action:
	default:
	} // 有界队列，连击不产生无界 goroutine。
}

func (d *Desktop) trayCommands(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case action := <-d.trayActions:
			d.trayCommand(action)
		}
	}
}

func (d *Desktop) trayNotice(message string) {
	d.showWindow(options.SecondInstanceData{})
	wruntime.EventsEmit(d.ctx, "desktop:message", message)
	_, _ = wruntime.MessageDialog(d.ctx, wruntime.MessageDialogOptions{Type: wruntime.InfoDialog, Title: "Kivo", Message: message, Buttons: []string{"知道了"}})
}

func (d *Desktop) trayConfirm(title, message string) bool {
	d.showWindow(options.SecondInstanceData{})
	answer, err := wruntime.MessageDialog(d.ctx, wruntime.MessageDialogOptions{Type: wruntime.QuestionDialog, Title: title, Message: message, Buttons: []string{"取消", "确认"}, DefaultButton: "取消", CancelButton: "取消"})
	return err == nil && answer == "确认"
}

// trayCommand 和页面复用相同 API 与互斥门；错误保留窗口，不强制接管或退出。
func (d *Desktop) trayCommand(action tray.Action) {
	if action == tray.OpenWeb {
		if err := d.OpenWeb(); err != "" {
			d.trayNotice(err)
		}
		return
	}
	if action == tray.QuitKeep || action == tray.QuitDisconnect {
		text := "关闭桌面与托盘，但后台、内核与当前代理继续运行。再次双击可打开界面。"
		if action == tray.QuitDisconnect {
			text = "恢复受管系统代理并停止内核，然后退出桌面与托盘；后台控制服务保留。恢复存在冲突时不会退出。"
		}
		if !d.trayConfirm("确认退出 Kivo 桌面", text) {
			return
		}
		if err := d.Quit(action == tray.QuitDisconnect); err != "" {
			d.trayNotice(err)
		}
		return
	}
	if action != tray.Connect && action != tray.Disconnect && action != tray.Check {
		return
	}
	done := d.beginOperation()
	if done == nil {
		d.trayNotice("其他操作正在进行，请稍后再试")
		return
	}
	defer done()
	var err error
	switch action {
	case tray.Connect:
		result := d.bridge.Request(d.ctx, "GET", "/api/v1/overview", "")
		var o app.Overview
		if result.Error != "" || json.Unmarshal(result.Data, &o) != nil {
			err = errors.New("无法读取最新代理状态，暂不连接")
			break
		}
		if !trayView(o, true, false, time.Now()).Connect {
			err = errors.New("当前不能接入，请在主窗口检查内核、系统代理与未恢复备份")
			break
		}
		body := "{}"
		if o.SystemProxy.State == "other" || o.SystemProxy.State == "automatic" {
			if !d.trayConfirm("替换当前系统代理？", "检测到其他代理或 PAC。确认后先备份，再由 Kivo 接入；断开时尝试恢复。不会强制覆盖恢复冲突。") {
				return
			}
			body = `{"replace":true}`
		}
		err = d.trayOperation("/api/v1/connection/connect", body)
	case tray.Disconnect:
		err = d.trayOperation("/api/v1/connection/disconnect", "{}")
	case tray.Check:
		err = d.trayOperation("/api/v1/connectivity/check", "{}")
		d.showWindow(options.SecondInstanceData{})
		wruntime.EventsEmit(d.ctx, "desktop:page", "data")
	}
	if err != nil {
		d.trayNotice(err.Error())
	}
	wruntime.EventsEmit(d.ctx, "desktop:refresh")
}

func (d *Desktop) trayOperation(path, body string) error {
	result := d.bridge.Request(d.ctx, "POST", path, body)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	if path == "/api/v1/connectivity/check" {
		return nil
	}
	var operation struct {
		Warning bool   `json:"warning"`
		Message string `json:"message"`
	}
	if json.Unmarshal(result.Data, &operation) != nil {
		return errors.New("无法确认操作结果，请查看主窗口")
	}
	if operation.Warning {
		return errors.New(operation.Message)
	}
	return nil
}
