//go:build !windows

package platform

import "os"

// EnableTerminalOutput 在 Unix 上不需修改模式；仅检查是否输出到真实终端。
func EnableTerminalOutput(output *os.File) (func(), bool) {
	return func() {}, TerminalWidth(output) > 0
}
