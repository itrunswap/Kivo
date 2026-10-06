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
	display := app.DisplayConnection(o, now)
	v.Title, v.Tone = display.Title, display.Tone
	v.Node = tray.Clean(o.Core.CurrentNode, 64)
	v.Connect, v.Disconnect = display.CanConnect, display.CanDisconnect
	v.Check = o.Core.State == "running" && o.ProxyPortListening
	v.Tip = "Kivo · " + v.Title + "\n节点：" + v.Node
	if busy {
		v.Tip = "Kivo · 操作进行中\n" + v.Title
	}
	return v
}

// startTray 使用一个受控循环管理重试；初次失败和驱动运行后丢失都可以恢复。
func (d *Desktop) startTray(ctx context.Context) {
	d.manageTray(ctx, tray.Start, func(attempt int) time.Duration { return time.Duration(attempt*attempt) * time.Second })
}

// manageTray 注入原生创建和退避时间，测试不依赖实际桌面宿主。
func (d *Desktop) manageTray(ctx context.Context, create func(context.Context, tray.Callbacks) (tray.Driver, error), delayFor func(int) time.Duration) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		d.mu.Lock()
		d.trayStarting = true
		d.mu.Unlock()
		initCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		driver, err := create(initCtx, tray.Callbacks{Action: d.queueTrayAction, Availability: d.trayAvailability})
		cancel()
		d.mu.Lock()
		d.trayStarting = false
		if err != nil {
			d.trayReady = false
			d.trayError = "托盘暂不可用，窗口仍可使用：" + err.Error()
		} else {
			d.tray, d.trayReady, d.trayError = driver, true, ""
		}
		d.mu.Unlock()
		if err == nil {
			attempt = 0
			d.runTray(ctx, driver)
			driver.Close()
			d.mu.Lock()
			d.tray = nil
			d.mu.Unlock()
			d.trayAvailability(false, "正在重新初始化托盘")
		}
		if ctx.Err() != nil {
			return
		}
		attempt++
		// 最多自动尝试五次，之后等待手动重试，避免长期空转。
		if attempt >= 5 {
			select {
			case <-ctx.Done():
				return
			case <-d.trayRetry:
				attempt = 0
			}
		} else {
			delay := time.NewTimer(delayFor(attempt))
			select {
			case <-ctx.Done():
				delay.Stop()
				return
			case <-d.trayRetry:
				delay.Stop()
				attempt = 0
			case <-delay.C:
			}
		}
	}
}

func (d *Desktop) runTray(ctx context.Context, driver tray.Driver) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	missing := 0
	for {
		d.refreshTray(ctx, driver)
		d.mu.RLock()
		ready := d.trayReady
		d.mu.RUnlock()
		if ready {
			missing = 0
		} else {
			missing++
		}
		if missing >= 3 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-d.trayRetry:
			return
		case <-ticker.C:
		case <-d.trayWake:
		}
	}
}

// RetryTray 不启动第二个托盘线程，只向生命周期循环发送一次有界重试请求。
func (d *Desktop) RetryTray() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.trayStarting {
		return "托盘正在初始化"
	}
	if d.trayReady {
		return ""
	}
	if d.trayRetry == nil {
		return "窗口尚未启动"
	}
	select {
	case d.trayRetry <- struct{}{}:
	default:
	}
	return ""
}

// updateTrayBusy 不等待 HTTP，让原生菜单立即反映事务互斥状态。
func (d *Desktop) updateTrayBusy() {
	d.mu.Lock()
	driver, view := d.tray, d.trayView
	view.Busy = d.inflight > 0 || d.quitting
	d.trayView = view
	d.mu.Unlock()
	if driver != nil {
		driver.Update(view)
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
		d.trayError = "托盘宿主不可用，主窗口已保留或恢复。" + reason
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
	result := d.readOverview(readCtx)
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
