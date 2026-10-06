//go:build windows

package platform

import (
	"os"
	"os/exec"
	"syscall"
)

// ConfigureCoreProcess 同时隐藏验证进程和常驻内核，避免从后台服务启动时弹出控制台。
func ConfigureCoreProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// SignalCoreStop 在 Windows 直接终止；os.Process.Signal 不支持 Interrupt，等待不会生效。
func SignalCoreStop(process *os.Process) error { return process.Kill() }
