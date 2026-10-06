package tray

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 使用独立的隐藏消息窗口。所有 Shell / 菜单 / 图标句柄只在此线程使用。
var user32 = windows.NewLazySystemDLL("user32.dll")
var shell32 = windows.NewLazySystemDLL("shell32.dll")
var registerClass = user32.NewProc("RegisterClassExW")
var unregisterClass = user32.NewProc("UnregisterClassW")
var createWindow = user32.NewProc("CreateWindowExW")
var destroyWindow = user32.NewProc("DestroyWindow")
var defWindowProc = user32.NewProc("DefWindowProcW")
var getMessage = user32.NewProc("GetMessageW")
var translateMessage = user32.NewProc("TranslateMessage")
var dispatchMessage = user32.NewProc("DispatchMessageW")
var postMessage = user32.NewProc("PostMessageW")
var postQuit = user32.NewProc("PostQuitMessage")
var notifyIcon = shell32.NewProc("Shell_NotifyIconW")
var createIcon = user32.NewProc("CreateIconFromResourceEx")
var destroyIcon = user32.NewProc("DestroyIcon")
var createMenu = user32.NewProc("CreatePopupMenu")
var appendMenu = user32.NewProc("AppendMenuW")
var destroyMenu = user32.NewProc("DestroyMenu")
var trackMenu = user32.NewProc("TrackPopupMenu")
var getCursor = user32.NewProc("GetCursorPos")
var foreground = user32.NewProc("SetForegroundWindow")
var registerMessage = user32.NewProc("RegisterWindowMessageW")
var getModule = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

const trayMessage = 0x8001
const updateMessage = 0x8002
const closeMessage = 0x0010

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type winPoint struct{ X, Y int32 }
type winMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          winPoint
	Private        uint32
}
type notification struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	Balloon             uintptr
}
type windowsTray struct {
	mu      sync.Mutex
	view    View
	cb      Callbacks
	window  atomic.Uintptr
	stopped atomic.Bool
	done    chan struct{}
	// 以下字段仅属于原生消息线程。
	nid            notification
	icons          map[string]uintptr
	added          bool
	taskbarMessage uint32
}

// Start 只有 Shell_NotifyIcon 真正成功后才返回可用驱动；失败不允许隐藏主窗口。
func Start(ctx context.Context, cb Callbacks) (Driver, error) {
	t := &windowsTray{cb: cb, view: View{Title: "正在连接后台", Tone: "idle"}, icons: map[string]uintptr{}, done: make(chan struct{})}
	ready := make(chan error, 1)
	go t.loop(ready)
	select {
	case err := <-ready:
		if err != nil {
			return nil, err
		}
		return t, nil
	case <-ctx.Done():
		t.Close()
		return nil, ctx.Err()
	}
}

func (t *windowsTray) Update(v View) {
	t.mu.Lock()
	t.view = v
	t.mu.Unlock()
	if window := t.window.Load(); window != 0 && !t.stopped.Load() {
		postMessage.Call(window, updateMessage, 0, 0)
	}
}
func (t *windowsTray) Close() {
	if !t.stopped.Swap(true) {
		if window := t.window.Load(); window != 0 {
			postMessage.Call(window, closeMessage, 0, 0)
		}
	}
	// 退出前等待消息线程删除 Shell 图标，避免留下需要鼠标划过才消失的幽灵托盘。
	select {
	case <-t.done:
	case <-time.After(time.Second):
	}
}
func (t *windowsTray) snapshot() View { t.mu.Lock(); defer t.mu.Unlock(); return t.view }

func (t *windowsTray) loop(ready chan<- error) {
	defer close(t.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("KivoTray-%d", windows.GetCurrentProcessId()))
	instance, _, _ := getModule.Call(0)
	wc := windowClass{Instance: instance, ClassName: name, Proc: windows.NewCallback(t.proc)}
	wc.Size = uint32(unsafe.Sizeof(wc))
	registered, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	if registered == 0 {
		ready <- fmt.Errorf("注册托盘消息窗口: %w", err)
		return
	}
	defer unregisterClass.Call(uintptr(unsafe.Pointer(name)), instance)
	window, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		ready <- fmt.Errorf("创建托盘消息窗口: %w", err)
		return
	}
	t.window.Store(window)
	defer func() {
		t.window.Store(0)
		if t.added {
			notifyIcon.Call(2, uintptr(unsafe.Pointer(&t.nid)))
		}
		for _, h := range t.icons {
			destroyIcon.Call(h)
		}
		destroyWindow.Call(window)
	}()
	if t.stopped.Load() {
		ready <- errors.New("托盘启动已取消")
		return
	}
	msg, _ := windows.UTF16PtrFromString("TaskbarCreated")
	nativeMsg, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(msg)))
	t.taskbarMessage = uint32(nativeMsg)
	t.nid = notification{Window: window, ID: 1, Flags: 7, Callback: trayMessage}
	t.nid.Size = uint32(unsafe.Sizeof(t.nid))
	if err := t.update(true); err != nil {
		ready <- err
		return
	}
	ready <- nil
	var m winMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	if !t.stopped.Load() {
		t.cb.Availability(false, "托盘消息循环已停止")
	}
}

func (t *windowsTray) update(add bool) error {
	v := t.snapshot()
	tone := v.Tone
	if v.Busy {
		tone = "busy"
	}
	icon := t.icons[tone]
	if icon == 0 {
		data := IconPNG(tone)
		var err error
		icon, _, err = createIcon.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, 32, 32, 0)
		runtime.KeepAlive(data)
		if icon == 0 {
			return fmt.Errorf("创建托盘图标: %w", err)
		}
		t.icons[tone] = icon
	}
	t.nid.Icon = icon
	tip := v.Tip
	if tip == "" {
		tip = "Kivo · " + v.Title
	}
	copy(t.nid.Tip[:], make([]uint16, 128))
	// 以 UTF-16 长度截断，不能把 emoji 切成半个代理对（surrogate pair）。
	u, _ := windows.UTF16FromString(tip)
	if len(u) > 128 {
		u = u[:127]
		if u[len(u)-1] >= 0xD800 && u[len(u)-1] <= 0xDBFF {
			u = u[:len(u)-1]
		}
	}
	copy(t.nid.Tip[:], u)
	operation := uintptr(1)
	if add || !t.added {
		operation = 0
	}
	result, _, err := notifyIcon.Call(operation, uintptr(unsafe.Pointer(&t.nid)))
	if result == 0 {
		t.added = false
		return fmt.Errorf("系统托盘暂不可用: %w", err)
	}
	t.added = true
	return nil
}

func (t *windowsTray) proc(window uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case closeMessage:
		destroyWindow.Call(window)
		return 0
	case 0x0002:
		postQuit.Call(0)
		return 0
	case updateMessage:
		err := t.update(false)
		if err != nil {
			t.cb.Availability(false, err.Error())
		} else {
			t.cb.Availability(true, "")
		}
		return 0
	case trayMessage:
		switch lparam {
		case 0x0202, 0x0203:
			t.cb.Action(Show)
		case 0x0205:
			t.menu()
		}
		return 0
	default:
		if msg == t.taskbarMessage && msg != 0 {
			err := t.update(true)
			if err != nil {
				t.cb.Availability(false, err.Error())
			} else {
				t.cb.Availability(true, "")
			}
			return 0
		}
	}
	result, _, _ := defWindowProc.Call(window, uintptr(msg), wparam, lparam)
	return result
}

func (t *windowsTray) menu() {
	h, _, _ := createMenu.Call()
	if h == 0 {
		return
	}
	defer destroyMenu.Call(h)
	for _, item := range Menu(t.snapshot()) {
		flags := uintptr(0)
		if item.Separator {
			flags = 0x800
		} else if !item.Enabled {
			flags = 1
		}
		label, _ := windows.UTF16PtrFromString(item.Label)
		appendMenu.Call(h, flags, uintptr(item.Action), uintptr(unsafe.Pointer(label)))
	}
	var p winPoint
	getCursor.Call(uintptr(unsafe.Pointer(&p)))
	foreground.Call(t.window.Load())
	// 返回菜单编号后异步派发，不在 Windows 原生窗口线程上等待 HTTP 或退出确认。
	id, _, _ := trackMenu.Call(h, 0x0100|0x0002, uintptr(p.X), uintptr(p.Y), 0, t.window.Load(), 0)
	postMessage.Call(t.window.Load(), 0, 0, 0)
	if id > 0 {
		t.cb.Action(Action(id))
	}
}
