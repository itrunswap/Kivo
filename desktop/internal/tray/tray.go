// Package tray 封装桌面原生托盘；不包含代理业务，也不接管 Wails 的主事件循环。
package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"unicode"
)

// Action 只允许固定菜单动作；原生回调不得直接执行长耗时代理事务。
type Action int32

const (
	Show Action = iota + 1
	Connect
	Disconnect
	Check
	OpenWeb
	QuitKeep
	QuitDisconnect
	EnableTUN
	DisableTUN
)

// View 是脱敏的、已派生的托盘视图，联网通过与仅接入使用不同颜色。
type View struct {
	Title, Node, Tip, Tone                                     string
	Connect, Disconnect, Check, TUNEnabled, TUNCanChange, Busy bool
}

// Item 同时供三个平台生成菜单，避免退出名称与可用条件漂移。
type Item struct {
	Action    Action
	Label     string
	Enabled   bool
	Separator bool
}

// Callbacks 返回可用性和用户意图；丢失宿主时必须让应用恢复窗口。
type Callbacks struct {
	Action       func(Action)
	Availability func(bool, string)
}

// Driver 的 Update 和 Close 必须支持跨 goroutine 调用，并在原生线程上操作 UI。
type Driver interface {
	Update(View)
	Close()
}

// Menu 仅显示一个代理开关，避免重开桌面时出现相互矛盾的开启/断开项。
func Menu(v View) []Item {
	connection := Item{Action: Connect, Label: "开启代理", Enabled: v.Connect && !v.Busy}
	if v.Disconnect {
		connection = Item{Action: Disconnect, Label: "关闭代理", Enabled: !v.Busy}
	}
	tun := Item{Action: EnableTUN, Label: "开启 TUN 模式", Enabled: v.TUNCanChange && !v.Busy}
	if v.TUNEnabled {
		tun = Item{Action: DisableTUN, Label: "关闭 TUN 模式", Enabled: v.TUNCanChange && !v.Busy}
	}
	return []Item{
		{Action: Show, Label: "显示主窗口", Enabled: true},
		{Label: "状态：" + v.Title}, {Label: "节点：" + Clean(v.Node, 64)},
		{Separator: true},
		connection,
		tun,
		{Action: Check, Label: "检测连通性", Enabled: v.Check && !v.Busy},
		{Action: OpenWeb, Label: "打开 Web 控制台", Enabled: true},
		{Separator: true},
		{Action: QuitKeep, Label: "退出桌面（保留代理）", Enabled: !v.Busy},
		{Action: QuitDisconnect, Label: "断开代理并退出桌面", Enabled: !v.Busy},
	}
}

// Clean 去除菜单快捷键标记及控制字符，防止长节点名破坏原生菜单和提示。
func Clean(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		if r == '&' {
			return '＆'
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	if len(runes) == 0 {
		return "未选择"
	}
	return string(runes)
}

// IconPNG 生成小尺寸原创 K 图标与状态角标，不写临时图标文件，也不使用默认应用图标。
func IconPNG(tone string) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	badge := color.NRGBA{148, 163, 184, 255}
	switch tone {
	case "active":
		badge = color.NRGBA{129, 140, 248, 255}
	case "good":
		badge = color.NRGBA{34, 197, 94, 255}
	case "error":
		badge = color.NRGBA{239, 68, 68, 255}
	case "busy", "warn":
		badge = color.NRGBA{245, 158, 11, 255}
	}
	for y := 2; y < 30; y++ {
		for x := 2; x < 30; x++ {
			if (x < 5 || x > 26) && (y < 5 || y > 26) {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{30, 32, 44, 255})
			if (x >= 9 && x <= 11 && y >= 8 && y <= 24) || (y >= 8 && y <= 16 && x >= 13 && x <= 23 && abs(x+y-30) <= 2) || (y >= 16 && y <= 24 && x >= 12 && x <= 23 && abs(x-y+3) <= 2) {
				img.SetNRGBA(x, y, color.NRGBA{165, 180, 252, 255})
			}
		}
	}
	for y := 21; y < 32; y++ {
		for x := 21; x < 32; x++ {
			d := (x-26)*(x-26) + (y-26)*(y-26)
			if d <= 30 {
				img.SetNRGBA(x, y, color.NRGBA{250, 250, 250, 255})
			}
			if d <= 17 {
				img.SetNRGBA(x, y, badge)
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img) // 内存缓冲编码无外部 IO。
	return out.Bytes()
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
