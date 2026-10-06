package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"
)

// desktopProxyBackend 使用系统自带工具，不经 shell 拼接命令，不在参数中传递代理密码。
// 平台选择放在独立构建文件；命令执行可注入，因此所有解析/恢复逻辑可跨平台测试。
type desktopProxyBackend struct {
	kind  string
	run   func(context.Context, string, ...string) (string, error)
	scope func() (string, error)
}

func newDesktopProxyBackend(kind string) SystemProxyBackend {
	return desktopProxyBackend{kind: kind, run: runProxyCommand, scope: func() (string, error) {
		u, err := user.Current()
		if err != nil {
			return "", errors.New("无法确认系统代理设置所属用户")
		}
		value := u.Uid
		if kind == "linux" {
			if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" || !strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "GNOME") {
				return "", errors.New("当前不是可确认的 GNOME 桌面会话；KDE/无桌面环境请使用 /proxy setup 或 TUN")
			}
			// GNOME 的 dconf 代理设置按用户持久化，并非按 DBus 临时地址保存。
			// 不把会话 GUID 写进 scope，避免注销/重启后无法恢复同一用户的原设置。
			value += ":gnome"
		}
		return value, nil
	}}
}

func runProxyCommand(ctx context.Context, executable string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	data, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("系统代理工具执行超时或已取消: %w", ctx.Err())
		}
		// 不转发 stderr/参数：其中可能含 PAC 地址、认证信息或原系统配置。
		return "", fmt.Errorf("系统代理工具 %s 执行失败；请检查权限、桌面会话及工具是否可用", executable)
	}
	if len(data) > 256<<10 {
		return "", errors.New("系统代理工具输出超出安全限制")
	}
	return strings.TrimSpace(string(data)), nil
}

var gnomeProxyKeys = []string{
	"org.gnome.system.proxy/mode", "org.gnome.system.proxy/autoconfig-url", "org.gnome.system.proxy/ignore-hosts",
	"org.gnome.system.proxy/use-same-proxy", "org.gnome.system.proxy.http/host", "org.gnome.system.proxy.http/port",
	"org.gnome.system.proxy.http/use-authentication", "org.gnome.system.proxy.https/host", "org.gnome.system.proxy.https/port",
}

func (b desktopProxyBackend) Capture(ctx context.Context) (ProxySnapshot, error) {
	scope, err := b.scope()
	if err != nil {
		return ProxySnapshot{}, err
	}
	result := ProxySnapshot{Backend: b.kind + "-desktop", Scope: scope, Entries: []ProxyEntry{}}
	if b.kind == "linux" {
		entry := ProxyEntry{Name: "gnome-session", Values: map[string]string{}}
		for _, path := range gnomeProxyKeys {
			schema, key, _ := strings.Cut(path, "/")
			value, err := b.run(ctx, "gsettings", "get", schema, key)
			if err != nil {
				return result, err
			}
			entry.Values[path] = value // 保留 GVariant 原文，避免绕过列表等类型信息丢失。
		}
		result.Entries = append(result.Entries, entry)
		return result, nil
	}
	services, err := b.run(ctx, "/usr/sbin/networksetup", "-listallnetworkservices")
	if err != nil {
		return result, err
	}
	for _, name := range strings.Split(services, "\n") {
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "*") || strings.HasPrefix(name, "An asterisk") {
			continue
		}
		entry := ProxyEntry{Name: name, Values: map[string]string{}}
		for _, item := range []struct{ flag, prefix string }{{"-getwebproxy", "http"}, {"-getsecurewebproxy", "https"}, {"-getsocksfirewallproxy", "socks"}, {"-getautoproxyurl", "pac"}} {
			output, err := b.run(ctx, "/usr/sbin/networksetup", item.flag, name)
			if err != nil {
				return result, err
			}
			values := parseNetworkSetup(output)
			if values["Enabled"] != "Yes" && values["Enabled"] != "No" {
				return result, errors.New("无法解析 macOS 代理开关，拒绝改写")
			}
			entry.Values[item.prefix+".enabled"] = values["Enabled"]
			if item.prefix == "pac" {
				entry.Values["pac.url"] = values["URL"]
			} else {
				entry.Values[item.prefix+".host"] = values["Server"]
				entry.Values[item.prefix+".port"] = values["Port"]
				entry.Values[item.prefix+".auth"] = values["Authenticated Proxy Enabled"]
			}
		}
		output, err := b.run(ctx, "/usr/sbin/networksetup", "-getproxyautodiscovery", name)
		if err != nil {
			return result, err
		}
		value := parseNetworkSetup(output)["Auto Proxy Discovery"]
		if value != "On" && value != "Off" {
			return result, errors.New("无法解析 macOS 自动发现设置，拒绝改写")
		}
		entry.Values["discovery"] = value
		result.Entries = append(result.Entries, entry)
	}
	if len(result.Entries) == 0 {
		return result, errors.New("没有可设置代理的已启用 macOS 网络服务")
	}
	slices.SortFunc(result.Entries, func(a, c ProxyEntry) int { return strings.Compare(a.Name, c.Name) })
	return result, nil
}

func parseNetworkSetup(output string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func (b desktopProxyBackend) Target(current ProxySnapshot, endpoint string) (ProxySnapshot, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return ProxySnapshot{}, errors.New("系统代理地址无效")
	}
	result := CloneProxySnapshot(current)
	for _, entry := range result.Entries {
		v := entry.Values
		if b.kind == "linux" {
			// 不触碰凭据本身；认证开关关闭后本地无认证代理才能正常工作。
			v["org.gnome.system.proxy/mode"] = "'manual'"
			v["org.gnome.system.proxy/autoconfig-url"] = "''"
			v["org.gnome.system.proxy/use-same-proxy"] = "false"
			v["org.gnome.system.proxy.http/use-authentication"] = "false"
			for _, protocol := range []string{"http", "https"} {
				v["org.gnome.system.proxy."+protocol+"/host"] = "'" + host + "'"
				v["org.gnome.system.proxy."+protocol+"/port"] = port
			}
		} else {
			for _, protocol := range []string{"http", "https"} {
				if v[protocol+".auth"] != "0" {
					return result, errors.New("macOS 原代理含认证或认证状态未知，无法完整备份 Keychain 凭据，拒绝覆盖")
				}
				v[protocol+".enabled"], v[protocol+".host"], v[protocol+".port"] = "Yes", host, port
			}
			v["pac.enabled"], v["discovery"] = "No", "Off"
		}
	}
	return result, nil
}

func (b desktopProxyBackend) Disabled(current ProxySnapshot) ProxySnapshot {
	result := CloneProxySnapshot(current)
	for _, entry := range result.Entries {
		if b.kind == "linux" {
			entry.Values["org.gnome.system.proxy/mode"] = "'none'"
		} else {
			entry.Values["http.enabled"], entry.Values["https.enabled"] = "No", "No"
		}
	}
	return result
}

func (b desktopProxyBackend) Apply(ctx context.Context, target ProxySnapshot) error {
	current, err := b.Capture(ctx)
	if err != nil {
		return err
	}
	if current.Backend != target.Backend || current.Scope != target.Scope {
		return errors.New("代理快照不属于当前用户或桌面会话")
	}
	for _, entry := range target.Entries {
		var actual *ProxyEntry
		for i := range current.Entries {
			if current.Entries[i].Name == entry.Name {
				actual = &current.Entries[i]
				break
			}
		}
		if actual == nil {
			return errors.New("原网络服务已不存在，备份仍保留")
		}
		if b.kind == "linux" {
			// 先写服务器字段，最后切换模式，尽量避免把请求送往未准备好的地址。
			keys := append(slices.Clone(gnomeProxyKeys[1:]), gnomeProxyKeys[0])
			for _, path := range keys {
				value, exists := entry.Values[path]
				if !exists {
					return errors.New("GNOME 代理备份不完整")
				}
				if value == actual.Values[path] {
					continue
				}
				schema, key, _ := strings.Cut(path, "/")
				writable, err := b.run(ctx, "gsettings", "writable", schema, key)
				if err != nil {
					return err
				}
				if writable != "true" {
					return errors.New("GNOME 代理受组织策略锁定，不能写入")
				}
				if _, err := b.run(ctx, "gsettings", "set", schema, key, value); err != nil {
					return err
				}
			}
			continue
		}
		for _, protocol := range []struct{ key, set, toggle string }{{"http", "-setwebproxy", "-setwebproxystate"}, {"https", "-setsecurewebproxy", "-setsecurewebproxystate"}} {
			v, a := entry.Values, actual.Values
			if v[protocol.key+".auth"] != "0" || a[protocol.key+".auth"] != "0" {
				return errors.New("macOS 认证代理不支持自动覆盖/恢复")
			}
			if v[protocol.key+".host"] != a[protocol.key+".host"] || v[protocol.key+".port"] != a[protocol.key+".port"] {
				port, err := strconv.Atoi(v[protocol.key+".port"])
				if err != nil || port < 0 || port > 65535 {
					return errors.New("macOS 代理备份端口无效")
				}
				if _, err := b.run(ctx, "/usr/sbin/networksetup", protocol.set, entry.Name, v[protocol.key+".host"], v[protocol.key+".port"]); err != nil {
					return fmt.Errorf("修改 macOS 代理失败，可能需要管理员权限: %w", err)
				}
			}
			state := "off"
			if v[protocol.key+".enabled"] == "Yes" {
				state = "on"
			} else if v[protocol.key+".enabled"] != "No" {
				return errors.New("macOS 代理备份开关无效")
			}
			// setwebproxy 可能自动开启，故最后总是显式恢复预期状态。
			if _, err := b.run(ctx, "/usr/sbin/networksetup", protocol.toggle, entry.Name, state); err != nil {
				return err
			}
		}
		v, a := entry.Values, actual.Values
		if v["pac.url"] != a["pac.url"] {
			if _, err := b.run(ctx, "/usr/sbin/networksetup", "-setautoproxyurl", entry.Name, v["pac.url"]); err != nil {
				return err
			}
		}
		pac := "off"
		if v["pac.enabled"] == "Yes" {
			pac = "on"
		} else if v["pac.enabled"] != "No" {
			return errors.New("macOS PAC 备份开关无效")
		}
		if _, err := b.run(ctx, "/usr/sbin/networksetup", "-setautoproxystate", entry.Name, pac); err != nil {
			return err
		}
		discovery := strings.ToLower(v["discovery"])
		if discovery != "on" && discovery != "off" {
			return errors.New("macOS 自动发现备份无效")
		}
		if _, err := b.run(ctx, "/usr/sbin/networksetup", "-setproxyautodiscovery", entry.Name, discovery); err != nil {
			return err
		}
	}
	return nil
}

func gvariantString(value string) string {
	if decoded, err := strconv.Unquote(value); err == nil {
		return decoded
	}
	return strings.Trim(value, "'")
}

func (b desktopProxyBackend) Inspect(snapshot ProxySnapshot, endpoint string) SystemProxyStatus {
	state := "this_app"
	if len(snapshot.Entries) == 0 {
		state = "unknown"
	}
	states := map[string]bool{}
	for _, entry := range snapshot.Entries {
		v := entry.Values
		current := "off"
		if b.kind == "linux" {
			switch gvariantString(v["org.gnome.system.proxy/mode"]) {
			case "auto":
				current = "automatic"
			case "manual":
				current = "other"
				host := gvariantString(v["org.gnome.system.proxy.https/host"])
				if proxyEndpointMatches(net.JoinHostPort(host, v["org.gnome.system.proxy.https/port"]), endpoint) {
					current = "this_app"
				}
			case "none":
				current = "off"
			default:
				current = "unknown"
			}
		} else if v["pac.enabled"] == "Yes" || v["discovery"] == "On" {
			current = "automatic"
		} else if v["https.enabled"] == "Yes" {
			current = "other"
			if proxyEndpointMatches(net.JoinHostPort(v["https.host"], v["https.port"]), endpoint) {
				current = "this_app"
			}
		} else if v["socks.enabled"] == "Yes" || v["http.enabled"] == "Yes" {
			current = "other"
		}
		states[current] = true
	}
	// 不依赖网络服务排列顺序；某服务已关代理不能掩盖另一服务的外部代理/PAC。
	if states["unknown"] {
		state = "unknown"
	} else if states["automatic"] {
		state = "automatic"
	} else if states["other"] || (states["off"] && states["this_app"]) {
		state = "other"
	} else if states["off"] {
		state = "off"
	}
	messages := map[string]string{"this_app": "已接入 · 系统 HTTPS 代理指向本程序", "off": "未接入 · 系统代理已关闭", "other": "未接入 · 存在其他代理设置", "automatic": "接入待确认 · 当前使用 PAC/自动发现", "unknown": "系统代理状态未确认"}
	return SystemProxyStatus{State: state, Message: messages[state], Supported: true}
}

func (b desktopProxyBackend) Lock(ctx context.Context) (func(), error) { return lockSystemProxy(ctx) }
