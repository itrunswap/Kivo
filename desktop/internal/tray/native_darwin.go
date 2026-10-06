package tray

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void kivoTrayStart(void);
void kivoTrayStop(void);
void kivoTrayBegin(char*, void*, int);
void kivoTrayItem(int, char*, int, int);
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"unsafe"
)

// AppKit 托盘复用 Wails 的主线程和 NSApplication，绝不替换其 delegate 或调用 NSApp.run/terminate。
type macTray struct {
	mu     sync.Mutex
	cb     Callbacks
	ready  chan bool
	closed bool
	view   View
}

var macOwner struct {
	sync.Mutex
	tray *macTray
}

func Start(ctx context.Context, cb Callbacks) (Driver, error) {
	t := &macTray{cb: cb, ready: make(chan bool, 1)}
	macOwner.Lock()
	if macOwner.tray != nil {
		macOwner.Unlock()
		return nil, errors.New("托盘已初始化")
	}
	macOwner.tray = t
	macOwner.Unlock()
	C.kivoTrayStart()
	select {
	case ok := <-t.ready:
		if !ok {
			t.Close()
			return nil, errors.New("系统菜单栏不可用")
		}
		return t, nil
	case <-ctx.Done():
		t.Close()
		return nil, ctx.Err()
	}
}

//export kivoTrayReady
func kivoTrayReady(ready C.int) {
	macOwner.Lock()
	t := macOwner.tray
	macOwner.Unlock()
	if t != nil {
		select {
		case t.ready <- ready != 0:
		default:
		}
	}
}

//export kivoTrayAction
func kivoTrayAction(action C.int) {
	macOwner.Lock()
	t := macOwner.tray
	macOwner.Unlock()
	if t != nil {
		t.cb.Action(Action(action))
	}
}

func (t *macTray) Update(v View) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.view == v {
		return
	}
	t.view = v
	tip := C.CString(v.Tip)
	defer C.free(unsafe.Pointer(tip))
	tone := v.Tone
	if v.Busy {
		tone = "busy"
	}
	png := IconPNG(tone)
	data := C.CBytes(png)
	defer C.free(data)
	C.kivoTrayBegin(tip, data, C.int(len(png)))
	for _, item := range Menu(v) {
		label := C.CString(item.Label)
		enabled, separator := 0, 0
		if item.Enabled {
			enabled = 1
		}
		if item.Separator {
			separator = 1
		}
		C.kivoTrayItem(C.int(item.Action), label, C.int(enabled), C.int(separator))
		C.free(unsafe.Pointer(label))
	}
}
func (t *macTray) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	macOwner.Lock()
	if macOwner.tray == t {
		macOwner.tray = nil
	}
	macOwner.Unlock()
	C.kivoTrayStop()
}
