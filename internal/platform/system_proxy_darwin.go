//go:build darwin

package platform

func NewSystemProxyBackend() SystemProxyBackend { return newDesktopProxyBackend("darwin") }
