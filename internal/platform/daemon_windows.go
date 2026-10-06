//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// StartDetached 在 Windows 上隐藏后台窗口并与当前控制台解耦。
func StartDetached(executable string, args []string, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开后台日志: %w", err)
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess, HideWindow: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动后台服务: %w", err)
	}
	_ = cmd.Process.Release()
	_ = logFile.Close()
	return nil
}
