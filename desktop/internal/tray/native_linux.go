package tray

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const notifier = "org.kde.StatusNotifierItem"
const watcher = "org.kde.StatusNotifierWatcher"
const dbusMenu = "com.canonical.dbusmenu"
const itemPath = dbus.ObjectPath("/StatusNotifierItem")
const menuPath = dbus.ObjectPath("/Menu")

// Linux 使用 StatusNotifierItem + DBusMenu，不另起 GTK 主循环；无宿主时保持窗口可见。
type linuxTray struct {
	mu         sync.Mutex
	conn       *dbus.Conn
	props      *prop.Properties
	view       View
	cb         Callbacks
	revision   uint32
	registered bool
	closed     bool
}
type pixmap struct {
	Width, Height int32
	Data          []byte
}
type tooltip struct {
	Icon               string
	Pixels             []pixmap
	Title, Description string
}
type layout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}
type groupProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}
type menuEvent struct {
	ID        int32
	Event     string
	Data      dbus.Variant
	Timestamp uint32
}
type notifierAPI struct{ tray *linuxTray }
type menuAPI struct{ tray *linuxTray }

func Start(ctx context.Context, cb Callbacks) (Driver, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, errors.New("无法连接桌面会话 D-Bus，保持普通窗口模式")
	}
	t := &linuxTray{conn: conn, cb: cb, view: View{Title: "正在连接后台"}, revision: 1}
	for _, export := range []struct {
		value any
		path  dbus.ObjectPath
		name  string
	}{{&notifierAPI{t}, itemPath, notifier}, {&menuAPI{t}, menuPath, dbusMenu}} {
		if err = conn.Export(export.value, export.path, export.name); err != nil {
			conn.Close()
			return nil, err
		}
	}
	properties := prop.Map{notifier: {
		"Category": {Value: "ApplicationStatus", Emit: prop.EmitConst}, "Id": {Value: "kivo", Emit: prop.EmitConst},
		"Title": {Value: "Kivo", Emit: prop.EmitTrue}, "Status": {Value: "Active", Emit: prop.EmitTrue},
		"WindowId": {Value: int32(0), Emit: prop.EmitConst}, "IconName": {Value: "", Emit: prop.EmitConst},
		"IconThemePath":   {Value: "", Emit: prop.EmitConst},
		"OverlayIconName": {Value: "", Emit: prop.EmitConst}, "OverlayIconPixmap": {Value: []pixmap{}, Emit: prop.EmitConst},
		"AttentionIconName": {Value: "", Emit: prop.EmitConst}, "AttentionIconPixmap": {Value: []pixmap{}, Emit: prop.EmitConst},
		"AttentionMovieName": {Value: "", Emit: prop.EmitConst},
		"IconPixmap":         {Value: linuxPixels("idle"), Emit: prop.EmitTrue}, "ToolTip": {Value: tooltip{Title: "Kivo"}, Emit: prop.EmitTrue},
		"ItemIsMenu": {Value: false, Emit: prop.EmitConst}, "Menu": {Value: menuPath, Emit: prop.EmitConst},
	}}
	t.props, err = prop.Export(conn, itemPath, properties)
	if err != nil {
		conn.Close()
		return nil, err
	}
	menuProps, err := prop.Export(conn, menuPath, prop.Map{dbusMenu: {"Version": {Value: uint32(3), Emit: prop.EmitConst}, "TextDirection": {Value: "ltr", Emit: prop.EmitConst}, "Status": {Value: "normal", Emit: prop.EmitConst}, "IconThemePath": {Value: []string{}, Emit: prop.EmitConst}}})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err = conn.Export(introspect.NewIntrospectable(&introspect.Node{Name: string(itemPath), Interfaces: []introspect.Interface{{Name: notifier, Methods: introspect.Methods(&notifierAPI{t}), Properties: t.props.Introspection(notifier)}, prop.IntrospectData}}), itemPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}
	if err = conn.Export(introspect.NewIntrospectable(&introspect.Node{Name: string(menuPath), Interfaces: []introspect.Interface{{Name: dbusMenu, Methods: introspect.Methods(&menuAPI{t}), Properties: menuProps.Introspection(dbusMenu)}, prop.IntrospectData}}), menuPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}
	if !t.register(ctx) {
		conn.Close()
		return nil, errors.New("桌面没有可用托盘宿主；GNOME 需启用 AppIndicator 支持，保持普通窗口模式")
	}
	return t, nil
}

func (t *linuxTray) register(ctx context.Context) bool {
	var owner bool
	if t.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, watcher).Store(&owner) != nil || !owner {
		return false
	}
	if !t.hostReady(ctx) {
		return false
	}
	service := t.conn.Names()[0]
	if t.conn.Object(watcher, "/StatusNotifierWatcher").CallWithContext(ctx, watcher+".RegisterStatusNotifierItem", 0, service).Err != nil {
		return false
	}
	t.registered = true
	return true
}

// 有 watcher 不等于有正在显示图标的面板宿主；两者都确认后才允许隐藏窗口。
func (t *linuxTray) hostReady(ctx context.Context) bool {
	var value dbus.Variant
	err := t.conn.Object(watcher, "/StatusNotifierWatcher").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, watcher, "IsStatusNotifierHostRegistered").Store(&value)
	return err == nil && value.Value() == true
}
func (t *linuxTray) Update(v View) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// godbus 的 SetMust 在发送属性信号失败时会 panic。这里只隔离原生适配器
	// 的断线边界，恢复窗口，不让桌面会话重启导致整个应用崩溃。
	defer func() {
		if recover() != nil {
			t.registered = false
			t.cb.Availability(false, "托盘 D-Bus 连接已断开")
		}
	}()
	if t.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var owner bool
	if t.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, watcher).Store(&owner) != nil || !owner || !t.hostReady(ctx) {
		t.registered = false
		t.cb.Availability(false, "托盘宿主已停止，已恢复主窗口")
		return
	}
	if !t.registered && !t.register(ctx) {
		t.cb.Availability(false, "无法重新注册托盘")
		return
	}
	t.cb.Availability(true, "")
	if t.view == v {
		return
	}
	tone := v.Tone
	if v.Busy {
		tone = "busy"
	}
	t.props.SetMust(notifier, "Title", "Kivo · "+v.Title)
	t.props.SetMust(notifier, "IconPixmap", linuxPixels(tone))
	t.props.SetMust(notifier, "ToolTip", tooltip{Title: "Kivo · " + v.Title, Description: v.Node})
	for _, signal := range []string{"NewTitle", "NewIcon", "NewToolTip"} {
		if t.conn.Emit(itemPath, notifier+"."+signal) != nil {
			t.registered = false
			t.cb.Availability(false, "托盘 D-Bus 连接已断开")
			return
		}
	}
	if t.conn.Emit(menuPath, dbusMenu+".LayoutUpdated", t.revision+1, int32(0)) != nil {
		t.registered = false
		t.cb.Availability(false, "托盘 D-Bus 连接已断开")
		return
	}
	t.view = v
	t.revision++
}
func (t *linuxTray) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		_ = t.conn.Close()
	}
}

func linuxPixels(tone string) []pixmap {
	img, _ := png.Decode(bytes.NewReader(IconPNG(tone)))
	data := make([]byte, 32*32*4)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			offset := (y*32 + x) * 4
			data[offset] = byte(a >> 8)
			data[offset+1] = byte(r >> 8)
			data[offset+2] = byte(g >> 8)
			data[offset+3] = byte(b >> 8)
		}
	}
	return []pixmap{{32, 32, data}}
}
func (n *notifierAPI) Activate(x, y int32) *dbus.Error                    { n.tray.cb.Action(Show); return nil }
func (n *notifierAPI) SecondaryActivate(x, y int32) *dbus.Error           { n.tray.cb.Action(Show); return nil }
func (n *notifierAPI) ContextMenu(x, y int32) *dbus.Error                 { return nil } // 宿主通过 Menu 属性取得 DBusMenu。
func (n *notifierAPI) Scroll(delta int32, orientation string) *dbus.Error { return nil }

func (t *linuxTray) items() map[int32]Item {
	items := map[int32]Item{}
	for index, item := range Menu(t.view) {
		id := int32(item.Action)
		if id == 0 {
			id = int32(100 + index)
		}
		items[id] = item
	}
	return items
}
func itemProperties(item Item) map[string]dbus.Variant {
	if item.Separator {
		return map[string]dbus.Variant{"type": dbus.MakeVariant("separator"), "visible": dbus.MakeVariant(true)}
	}
	return map[string]dbus.Variant{"label": dbus.MakeVariant(item.Label), "enabled": dbus.MakeVariant(item.Enabled), "visible": dbus.MakeVariant(true)}
}
func (m *menuAPI) GetLayout(parent int32, depth int32, properties []string) (uint32, layout, *dbus.Error) {
	t := m.tray
	t.mu.Lock()
	defer t.mu.Unlock()
	if parent != 0 {
		item, ok := t.items()[parent]
		if !ok {
			return 0, layout{}, dbus.NewError("com.canonical.dbusmenu.InvalidMenuId", nil)
		}
		return t.revision, layout{ID: parent, Properties: itemProperties(item), Children: []dbus.Variant{}}, nil
	}
	root := layout{Properties: map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}, Children: []dbus.Variant{}}
	if depth != 0 {
		for index, item := range Menu(t.view) {
			id := int32(item.Action)
			if id == 0 {
				id = int32(100 + index)
			}
			root.Children = append(root.Children, dbus.MakeVariant(layout{ID: id, Properties: itemProperties(item), Children: []dbus.Variant{}}))
		}
	}
	return t.revision, root, nil
}
func (m *menuAPI) GetGroupProperties(ids []int32, names []string) ([]groupProperties, *dbus.Error) {
	t := m.tray
	t.mu.Lock()
	defer t.mu.Unlock()
	result := []groupProperties{}
	for id, item := range t.items() {
		if len(ids) > 0 {
			found := false
			for _, wanted := range ids {
				if id == wanted {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, groupProperties{id, itemProperties(item)})
	}
	return result, nil
}
func (m *menuAPI) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	t := m.tray
	t.mu.Lock()
	defer t.mu.Unlock()
	if item, ok := t.items()[id]; ok {
		if value, ok := itemProperties(item)[name]; ok {
			return value, nil
		}
	}
	return dbus.Variant{}, dbus.NewError("com.canonical.dbusmenu.InvalidMenuId", nil)
}
func (m *menuAPI) Event(id int32, event string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if event != "clicked" {
		return nil
	}
	t := m.tray
	t.mu.Lock()
	item, ok := t.items()[id]
	t.mu.Unlock()
	if ok && item.Enabled && item.Action != 0 {
		t.cb.Action(item.Action)
	}
	return nil
}
func (m *menuAPI) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, event := range events {
		m.Event(event.ID, event.Event, event.Data, event.Timestamp)
	}
	return []int32{}, nil
}
func (m *menuAPI) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }
func (m *menuAPI) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}
