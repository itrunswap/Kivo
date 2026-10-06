//go:build windows

package main

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 无控制台的程序在窗口初始化前出错也要给出可见提示；serve 子进程只记录日志。
func showStartupError(message string) {
	for _, arg := range os.Args[1:] {
		if arg == "serve" || arg == "--smoke-test" {
			return
		}
	}
	text, _ := windows.UTF16PtrFromString("Kivo 桌面端无法启动：\n\n" + strings.ReplaceAll(message, "\x00", "") + "\n\n请检查数据目录中的 config.json 和 logs/controller.log。")
	title, _ := windows.UTF16PtrFromString("Kivo · 启动失败")
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
