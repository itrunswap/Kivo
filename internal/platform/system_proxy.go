package platform

import (
	"context"
	"errors"
	"maps"
	"reflect"
)

// ProxySnapshot 只在本地配置中保存，不经公开 API 返回；PAC 地址等可能含有凭据。
// Scope 绑定操作系统用户/桌面会话，避免使用另一用户或会话的备份进行恢复。
type ProxySnapshot struct {
	Backend string       `json:"backend"`
	Scope   string       `json:"scope"`
	Entries []ProxyEntry `json:"entries"`
}

// ProxyEntry 是一个可独立恢复的设置单元，例如 Windows 当前连接或 macOS 网络服务。
type ProxyEntry struct {
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
}

// SystemProxyBackend 将平台命令与业务事务隔离，测试可注入纯内存实现。
// Apply 必须支持只应用指定 Entries；Lock 防止不同数据目录的本程序实例并发设置代理。
type SystemProxyBackend interface {
	Capture(context.Context) (ProxySnapshot, error)
	Target(ProxySnapshot, string) (ProxySnapshot, error)
	Disabled(ProxySnapshot) ProxySnapshot
	Apply(context.Context, ProxySnapshot) error
	Inspect(ProxySnapshot, string) SystemProxyStatus
	Lock(context.Context) (func(), error)
}

func CloneProxySnapshot(snapshot ProxySnapshot) ProxySnapshot {
	result := snapshot
	result.Entries = make([]ProxyEntry, len(snapshot.Entries))
	for i, entry := range snapshot.Entries {
		result.Entries[i] = ProxyEntry{Name: entry.Name, Values: maps.Clone(entry.Values)}
	}
	return result
}

func EqualProxySnapshots(a, b ProxySnapshot) bool { return reflect.DeepEqual(a, b) }

// ProxyRestorePlan 仅恢复本程序实际改动的字段，保留别的程序后来修改的字段。
// 同一设置单元的任意受管字段发生外部变化时，整个单元不写入，防止组合出错误配置。
// 允许恢复一半应用的事务，因此进程崩溃或平台命令部分失败仍可安全恢复。
func ProxyRestorePlan(current, before, applied ProxySnapshot, force bool) (ProxySnapshot, []string, error) {
	plan := ProxySnapshot{Backend: current.Backend, Scope: current.Scope, Entries: []ProxyEntry{}}
	if current.Backend != before.Backend || before.Backend != applied.Backend || current.Scope != before.Scope || before.Scope != applied.Scope {
		return plan, nil, errors.New("系统代理备份不属于当前平台、用户或桌面会话，拒绝恢复")
	}
	find := func(entries []ProxyEntry, name string) (ProxyEntry, bool) {
		for _, entry := range entries {
			if entry.Name == name {
				return entry, true
			}
		}
		return ProxyEntry{}, false
	}
	var conflicts []string
	for _, target := range applied.Entries {
		original, oldOK := find(before.Entries, target.Name)
		actual, nowOK := find(current.Entries, target.Name)
		if !oldOK {
			return plan, nil, errors.New("系统代理备份不完整，拒绝恢复")
		}
		if !nowOK {
			conflicts = append(conflicts, target.Name)
			continue
		}
		restore := ProxyEntry{Name: actual.Name, Values: maps.Clone(actual.Values)}
		conflict, changed := false, false
		for key, expected := range target.Values {
			oldValue, exists := original.Values[key]
			if !exists {
				return plan, nil, errors.New("系统代理备份缺少字段，拒绝恢复")
			}
			if oldValue == expected {
				continue
			}
			value, exists := actual.Values[key]
			if !exists || (!force && value != expected && value != oldValue) {
				conflict = true
				break
			}
			if value != oldValue {
				restore.Values[key], changed = oldValue, true
			}
		}
		if conflict {
			conflicts = append(conflicts, target.Name)
		} else if changed {
			plan.Entries = append(plan.Entries, restore)
		}
	}
	return plan, conflicts, nil
}
