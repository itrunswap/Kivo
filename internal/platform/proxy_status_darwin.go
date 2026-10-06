//go:build darwin

package platform

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// ReadSystemProxy 只读取当前网络的系统代理配置，不尝试执行 PAC 或修改网络设置。
func ReadSystemProxy(ctx context.Context, expected string) SystemProxyStatus {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return SystemProxyStatus{State: "unknown", Message: "无法读取 macOS 系统代理配置"}
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " : ")
		if ok {
			values[key] = value
		}
	}
	if values["ProxyAutoConfigEnable"] == "1" || values["ProxyAutoDiscoveryEnable"] == "1" {
		return SystemProxyStatus{State: "automatic", Message: "接入未确认 · 自动代理/PAC 决定实际路径"}
	}
	if values["HTTPSEnable"] != "1" {
		return SystemProxyStatus{State: "off", Message: "未接入 · 系统 HTTPS 代理已禁用（TUN/独立设置除外）"}
	}
	if proxyEndpointMatches(values["HTTPSProxy"]+":"+values["HTTPSPort"], expected) {
		return SystemProxyStatus{State: "this_app", Message: "已接入 · 系统 HTTPS 代理指向本程序"}
	}
	return SystemProxyStatus{State: "other", Message: "未接入 · 系统 HTTPS 代理未指向本程序"}
}
