//go:build linux

package platform

import "context"

func ReadSystemProxy(_ context.Context, _ string) SystemProxyStatus {
	// Linux 桌面、浏览器和环境变量各自维护代理设置，不能从服务进程环境推断浏览器。
	return SystemProxyStatus{State: "unknown", Message: "Linux 无统一系统代理状态，请检查浏览器代理设置"}
}
