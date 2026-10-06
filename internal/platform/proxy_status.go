package platform

import (
	"net"
	"net/url"
	"strings"
)

// SystemProxyStatus 仅描述操作系统的代理配置，不代表浏览器扩展、PAC 最终选择或网络连通性。
type SystemProxyStatus struct {
	State           string `json:"state"` // this_app / other / off / automatic / unknown
	Message         string `json:"message"`
	Supported       bool   `json:"supported"`
	Managed         bool   `json:"managed"`
	RecoveryPending bool   `json:"recoveryPending"`
	Conflict        bool   `json:"conflict"`
}

func proxyEndpointMatches(value, expected string) bool {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return false
	}
	host, port, err := net.SplitHostPort(expected)
	if err != nil || u.Port() != port {
		return false
	}
	return strings.EqualFold(u.Hostname(), host) || ((host == "127.0.0.1" || host == "::1") && strings.EqualFold(u.Hostname(), "localhost"))
}

func windowsProxyStatus(proxy string, automatic bool, expected string) SystemProxyStatus {
	if automatic {
		return SystemProxyStatus{State: "automatic", Message: "接入未确认 · 自动代理/PAC 决定实际路径"}
	}
	if strings.TrimSpace(proxy) == "" {
		return SystemProxyStatus{State: "off", Message: "未接入 · 系统代理已禁用（TUN/浏览器独立设置除外）"}
	}
	endpoint := proxy
	if strings.Contains(proxy, "=") {
		endpoint = ""
		for _, part := range strings.Split(proxy, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && strings.EqualFold(key, "https") {
				endpoint = value
				break
			}
		}
	}
	if proxyEndpointMatches(endpoint, expected) {
		return SystemProxyStatus{State: "this_app", Message: "已接入 · 系统 HTTPS 代理指向本程序"}
	}
	return SystemProxyStatus{State: "other", Message: "未接入 · 系统 HTTPS 代理未指向本程序"}
}
