//go:build windows

package platform

import (
	"context"
	"syscall"
	"unsafe"
)

var winHTTPProxyConfig = syscall.NewLazyDLL("winhttp.dll").NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var proxyGlobalFree = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalFree")

type ieProxyConfig struct {
	AutoDetect    int32
	AutoConfigURL *uint16
	Proxy         *uint16
	Bypass        *uint16
}

// ReadSystemProxy 读取当前服务用户的 Windows Internet Settings，不修改任何系统代理设置。
func ReadSystemProxy(_ context.Context, expected string) SystemProxyStatus {
	var cfg ieProxyConfig
	ok, _, _ := winHTTPProxyConfig.Call(uintptr(unsafe.Pointer(&cfg)))
	if ok == 0 {
		return SystemProxyStatus{State: "unknown", Message: "无法读取当前用户的系统代理配置"}
	}
	defer func() {
		for _, p := range []*uint16{cfg.AutoConfigURL, cfg.Proxy, cfg.Bypass} {
			if p != nil {
				proxyGlobalFree.Call(uintptr(unsafe.Pointer(p)))
			}
		}
	}()
	return windowsProxyStatus(proxyUTF16(cfg.Proxy), cfg.AutoDetect != 0 || proxyUTF16(cfg.AutoConfigURL) != "", expected)
}

func proxyUTF16(p *uint16) string {
	if p == nil {
		return ""
	}
	var chars []uint16
	for offset := uintptr(0); offset < 32768; offset++ {
		char := *(*uint16)(unsafe.Add(unsafe.Pointer(p), offset*2))
		if char == 0 {
			break
		}
		chars = append(chars, char)
	}
	return syscall.UTF16ToString(chars)
}
