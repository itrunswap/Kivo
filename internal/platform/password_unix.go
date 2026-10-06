//go:build !windows

package platform

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
)

// ReadPassword 使用系统 stty 临时关闭终端回显；失败时由调用方回退到普通读取。
func ReadPassword() ([]byte, error) {
	disable := exec.Command("stty", "-echo")
	disable.Stdin = os.Stdin
	if err := disable.Run(); err != nil {
		return nil, err
	}
	defer func() {
		enable := exec.Command("stty", "echo")
		enable.Stdin = os.Stdin
		_ = enable.Run()
	}()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return []byte(strings.TrimRight(line, "\r\n")), err
}
