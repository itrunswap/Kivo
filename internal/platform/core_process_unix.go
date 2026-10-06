//go:build !windows

package platform

import (
	"os"
	"os/exec"
)

// ConfigureCoreProcess 在 Unix 保留默认进程属性，不需要创建额外控制台。
func ConfigureCoreProcess(_ *exec.Cmd) {}

// SignalCoreStop 优先发出可供内核清理资源的中断信号。
func SignalCoreStop(process *os.Process) error { return process.Signal(os.Interrupt) }
