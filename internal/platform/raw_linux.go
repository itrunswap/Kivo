//go:build linux

package platform

import (
	"os"
	"syscall"
	"unsafe"
)

// StartRawMode 将 Linux TTY 切换为逐键读取模式。
func StartRawMode(input, output *os.File) (restore func(), interactive bool, err error) {
	inputFD := input.Fd()
	outputFD := output.Fd()
	var original syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, inputFD, syscall.TCGETS, uintptr(unsafe.Pointer(&original))); errno != 0 {
		return func() {}, false, nil
	}
	var outputState syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, outputFD, syscall.TCGETS, uintptr(unsafe.Pointer(&outputState))); errno != 0 {
		return func() {}, false, nil
	}
	raw := original
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	// 保留输出换行处理，否则 LF 不回到行首，候选菜单无法覆盖上一帧。
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, inputFD, syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return func() {}, false, errno
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, inputFD, syscall.TCSETS, uintptr(unsafe.Pointer(&original)))
	}, true, nil
}

type terminalSize struct {
	Row    uint16
	Column uint16
	XPixel uint16
	YPixel uint16
}

// TerminalWidth 返回 Linux 终端当前可见列数。
func TerminalWidth(output *os.File) int {
	var size terminalSize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, output.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		return 0
	}
	return int(size.Column)
}
