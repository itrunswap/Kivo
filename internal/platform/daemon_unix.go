//go:build !windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// StartDetached 启动与当前终端会话解耦的后台控制进程。
func StartDetached(executable string, args []string, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开后台日志: %w", err)
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动后台服务: %w", err)
	}
	_ = cmd.Process.Release()
	_ = logFile.Close()
	return nil
}
