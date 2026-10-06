//go:build !darwin

package main

import "github.com/wailsapp/wails/v2/pkg/menu"

// Windows / Linux 通过托盘与设置页提供退出，不增加占用窗口空间的顶部菜单栏。
func desktopMenu(*Desktop) *menu.Menu { return nil }
