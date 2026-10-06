//go:build !windows

package main

// macOS / Linux 的启动器会记录 stderr；后台启动失败也会在页面提供重试入口。
func showStartupError(string) {}
