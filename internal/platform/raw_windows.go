//go:build windows

package platform

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	enableProcessedInput        = 0x0001
	enableLineInput             = 0x0002
	enableVirtualTerminalInput  = 0x0200
	enableVirtualTerminalOutput = 0x0004
)

var getConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")

// EnableTerminalOutput 只启用控制台输出能力，不改变输入模式。
// 安装期间不在 raw mode 中，也必须能原位刷新进度；重定向输出时自动降级。
func EnableTerminalOutput(output *os.File) (func(), bool) {
	var mode uint32
	if ok, _, _ := getConsoleMode.Call(output.Fd(), uintptr(unsafe.Pointer(&mode))); ok == 0 {
		return func() {}, false
	}
	if ok, _, _ := setConsoleMode.Call(output.Fd(), uintptr(mode|enableVirtualTerminalOutput)); ok == 0 {
		return func() {}, false
	}
	return func() { setConsoleMode.Call(output.Fd(), uintptr(mode)) }, true
}

type consoleCoordinate struct {
	X int16
	Y int16
}

type consoleRectangle struct {
	Left   int16
	Top    int16
	Right  int16
	Bottom int16
}

type consoleScreenBufferInfo struct {
	Size              consoleCoordinate
	CursorPosition    consoleCoordinate
	Attributes        uint16
	Window            consoleRectangle
	MaximumWindowSize consoleCoordinate
}

// StartRawMode 将 Windows 控制台切换为逐键读取模式，并启用 ANSI 光标控制。
// 第二个返回值表示当前输入输出是否都是控制台；重定向场景应回退到普通行读取。
func StartRawMode(input, output *os.File) (restore func(), interactive bool, err error) {
	inputHandle := syscall.Handle(input.Fd())
	outputHandle := syscall.Handle(output.Fd())
	var inputMode, outputMode uint32
	if ok, _, _ := getConsoleMode.Call(uintptr(inputHandle), uintptr(unsafePointer(&inputMode))); ok == 0 {
		return func() {}, false, nil
	}
	if ok, _, _ := getConsoleMode.Call(uintptr(outputHandle), uintptr(unsafePointer(&outputMode))); ok == 0 {
		return func() {}, false, nil
	}

	newOutputMode := outputMode | enableVirtualTerminalOutput
	if ok, _, _ := setConsoleMode.Call(uintptr(outputHandle), uintptr(newOutputMode)); ok == 0 {
		return func() {}, false, errors.New("无法启用 Windows 控制台 ANSI 输出")
	}
	newInputMode := inputMode &^ (enableProcessedInput | enableLineInput | enableEchoInput)
	// Windows Terminal 支持 VT 输入，可让方向键与 Unix 使用同一套转义序列。
	if ok, _, _ := setConsoleMode.Call(uintptr(inputHandle), uintptr(newInputMode|enableVirtualTerminalInput)); ok == 0 {
		// 旧版控制台不支持 VT 输入时仍可使用输入、退格和 Tab 补全。
		if ok, _, _ = setConsoleMode.Call(uintptr(inputHandle), uintptr(newInputMode)); ok == 0 {
			setConsoleMode.Call(uintptr(outputHandle), uintptr(outputMode))
			return func() {}, false, errors.New("无法切换 Windows 控制台输入模式")
		}
	}

	restore = func() {
		setConsoleMode.Call(uintptr(inputHandle), uintptr(inputMode))
		setConsoleMode.Call(uintptr(outputHandle), uintptr(outputMode))
	}
	return restore, true, nil
}

// TerminalWidth 返回 Windows 控制台窗口的可见宽度，而不是后台缓冲区宽度。
func TerminalWidth(output *os.File) int {
	var info consoleScreenBufferInfo
	ok, _, _ := getConsoleScreenBufferInfo.Call(output.Fd(), uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}
