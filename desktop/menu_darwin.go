package main

import (
	"github.com/itrunswap/Kivo/desktop/internal/tray"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
)

// macOS 的 Cmd+Q 是真正退出，不能与窗口关闭共用“隐藏到托盘”逻辑。
func desktopMenu(d *Desktop) *menu.Menu {
	result := menu.NewMenu()
	app := result.AddSubmenu("Kivo")
	app.AddText("显示主窗口", nil, func(*menu.CallbackData) { d.queueTrayAction(tray.Show) })
	app.AddSeparator()
	app.AddText("退出桌面（保留代理）", keys.CmdOrCtrl("q"), func(*menu.CallbackData) { d.queueTrayAction(tray.QuitKeep) })
	app.AddText("断开代理并退出桌面", nil, func(*menu.CallbackData) { d.queueTrayAction(tray.QuitDisconnect) })
	result.Append(menu.EditMenu()) // 保留原生复制 / 粘贴，避免自定义菜单破坏订阅表单。
	result.Append(menu.WindowMenu())
	return result
}
