//go:build linux

package platform

func NewSystemProxyBackend() SystemProxyBackend { return newDesktopProxyBackend("linux") }
