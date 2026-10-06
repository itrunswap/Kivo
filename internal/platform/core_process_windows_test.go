//go:build windows

package platform

import (
	"os/exec"
	"testing"
)

func TestCoreProcessNeverCreatesConsole(t *testing.T) {
	for _, args := range [][]string{{"-v"}, {"-t", "-d", "runtime"}, {"-d", "runtime"}} {
		cmd := exec.Command("mihomo.exe", args...)
		ConfigureCoreProcess(cmd)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
			t.Fatal("core commands must hide their window and set CREATE_NO_WINDOW")
		}
	}
}
