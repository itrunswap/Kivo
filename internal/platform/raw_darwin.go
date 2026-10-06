//go:build darwin

package platform

import (
	"os"
	"syscall"
	"unsafe"
)

// StartRawMode 将 macOS TTY 切换为逐键读取模式。
func StartRawMode(input, output *os.File) (restore func(), interactive bool, err error) {
	inputFD := input.Fd()
	outputFD := output.Fd()
	var original syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, inputFD, syscall.TIOCGETA, uintptr(unsafe.Pointer(&original))); errno != 0 {
		return func() {}, false, nil
	}
	var outputState syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, outputFD, syscall.TIOCGETA, uintptr(unsafe.Pointer(&outputState))); errno != 0 {
		return func() {}, false, nil
	}
	raw := original
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	// 只把输入切换为逐键模式，保留 OPOST/ONLCR 输出处理。
	// 菜单渲染依赖换行后回到行首；关闭 OPOST 会让 macOS 每次重绘产生阶梯状残行。
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, inputFD, syscall.TIOCSETA, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return func() {}, false, errno
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, inputFD, syscall.TIOCSETA, uintptr(unsafe.Pointer(&original)))
	}, true, nil
}

type terminalSize struct {
	Row    uint16
	Column uint16
	XPixel uint16
	YPixel uint16
}

// TerminalWidth 返回 macOS 终端当前可见列数，读取失败时由调用方使用保守默认值。
func TerminalWidth(output *os.File) int {
	var size terminalSize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, output.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		return 0
	}
	return int(size.Column)
}
