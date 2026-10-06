//go:build windows

package platform

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const enableEchoInput = 0x0004

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode = kernel32.NewProc("GetConsoleMode")
	setConsoleMode = kernel32.NewProc("SetConsoleMode")
)

// ReadPassword 从 Windows 控制台读取一行并临时关闭回显。
func ReadPassword() ([]byte, error) {
	handle := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	result, _, _ := getConsoleMode.Call(uintptr(handle), uintptr(unsafePointer(&mode)))
	if result == 0 {
		return nil, errors.New("标准输入不是 Windows 控制台")
	}
	if ok, _, _ := setConsoleMode.Call(uintptr(handle), uintptr(mode&^enableEchoInput)); ok == 0 {
		return nil, errors.New("无法关闭控制台回显")
	}
	defer setConsoleMode.Call(uintptr(handle), uintptr(mode))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return []byte(strings.TrimRight(line, "\r\n")), err
}

// unsafePointer 集中隔离标准库 Windows API 所需的指针转换。
func unsafePointer(value *uint32) unsafe.Pointer { return unsafe.Pointer(value) }
