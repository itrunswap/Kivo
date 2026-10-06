//go:build windows

package platform

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	winInet           = syscall.NewLazyDLL("wininet.dll")
	queryProxyOption  = winInet.NewProc("InternetQueryOptionW")
	setProxyOption    = winInet.NewProc("InternetSetOptionW")
	proxyCreateMutex  = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	proxyWaitMutex    = syscall.NewLazyDLL("kernel32.dll").NewProc("WaitForSingleObject")
	proxyReleaseMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("ReleaseMutex")
)

// winProxyOption 的 Value 在本项目支持的 Windows amd64/arm64 上是八字节联合体。
type winProxyOption struct {
	Option uint32
	Value  uintptr
}

type winProxyOptionList struct {
	Size       uint32
	Connection uintptr
	Count      uint32
	Error      uint32
	Options    uintptr
}

type windowsProxyBackend struct{}

func lockSystemProxy(ctx context.Context) (func(), error) { return (windowsProxyBackend{}).Lock(ctx) }

func NewSystemProxyBackend() SystemProxyBackend { return windowsProxyBackend{} }

func windowsProxyScope() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("读取 Windows 用户身份: %w", err)
	}
	return u.Uid, nil // Windows 的 Uid 为用户 SID，不使用可更改的显示名称。
}

func (windowsProxyBackend) Capture(ctx context.Context) (ProxySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ProxySnapshot{}, err
	}
	scope, err := windowsProxyScope()
	if err != nil {
		return ProxySnapshot{}, err
	}
	options := []winProxyOption{{Option: 1}, {Option: 2}, {Option: 3}, {Option: 4}}
	list := winProxyOptionList{Size: uint32(unsafe.Sizeof(winProxyOptionList{})), Count: uint32(len(options)), Options: uintptr(unsafe.Pointer(&options[0]))}
	size := list.Size
	ok, _, callErr := queryProxyOption.Call(0, 75, uintptr(unsafe.Pointer(&list)), uintptr(unsafe.Pointer(&size)))
	defer func() {
		for _, option := range options[1:] {
			if option.Value != 0 {
				proxyGlobalFree.Call(option.Value)
			}
		}
	}()
	runtime.KeepAlive(options)
	if ok == 0 {
		return ProxySnapshot{}, fmt.Errorf("读取 Windows 当前用户代理失败: %w", callErr)
	}
	values := map[string]string{"flags": strconv.FormatUint(uint64(options[0].Value), 10), "server": "", "bypass": "", "pac": ""}
	for i, key := range []string{"server", "bypass", "pac"} {
		// WinInet 在联合体中返回已分配的字符串指针；直接从联合体读取指针类型，
		// 不进行 uintptr -> pointer 的往返转换，避免 Go checkptr/vet 的不安全转换。
		values[key] = proxyUTF16(*(**uint16)(unsafe.Pointer(&options[i+1].Value)))
	}
	return ProxySnapshot{Backend: "windows-wininet", Scope: scope, Entries: []ProxyEntry{{Name: "current-user", Values: values}}}, nil
}

func (windowsProxyBackend) Target(current ProxySnapshot, endpoint string) (ProxySnapshot, error) {
	result := CloneProxySnapshot(current)
	if len(result.Entries) != 1 {
		return result, fmt.Errorf("Windows 代理设置单元不完整")
	}
	result.Entries[0].Values["flags"] = "3" // DIRECT | PROXY；不会把 WinHTTP 当作浏览器代理。
	result.Entries[0].Values["server"] = endpoint
	result.Entries[0].Values["pac"] = ""
	return result, nil // 保留原绕过列表，不擅自清空公司/局域网配置。
}

func (windowsProxyBackend) Disabled(current ProxySnapshot) ProxySnapshot {
	result := CloneProxySnapshot(current)
	for _, entry := range result.Entries {
		flags, _ := strconv.ParseUint(entry.Values["flags"], 10, 32)
		entry.Values["flags"] = strconv.FormatUint((flags&^2)|1, 10)
	}
	return result
}

func (windowsProxyBackend) Apply(ctx context.Context, snapshot ProxySnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scope, err := windowsProxyScope()
	if err != nil {
		return err
	}
	if snapshot.Backend != "windows-wininet" || snapshot.Scope != scope {
		return fmt.Errorf("拒绝写入其他平台或用户的代理设置")
	}
	for _, entry := range snapshot.Entries {
		if entry.Name != "current-user" {
			return fmt.Errorf("不支持的 Windows 代理设置单元")
		}
		flags, err := strconv.ParseUint(entry.Values["flags"], 10, 32)
		if err != nil || flags&^uint64(15) != 0 {
			return fmt.Errorf("Windows 代理标志无效")
		}
		options := []winProxyOption{{Option: 1, Value: uintptr(flags)}}
		stringsW := make([][]uint16, 0, 3)
		for i, key := range []string{"server", "bypass", "pac"} {
			value, ok := entry.Values[key]
			if !ok {
				return fmt.Errorf("Windows 代理备份缺少字段")
			}
			wide, err := syscall.UTF16FromString(value)
			if err != nil {
				return fmt.Errorf("Windows 代理字段含非法字符")
			}
			stringsW = append(stringsW, wide)
			options = append(options, winProxyOption{Option: uint32(i + 2), Value: uintptr(unsafe.Pointer(&wide[0]))})
		}
		list := winProxyOptionList{Size: uint32(unsafe.Sizeof(winProxyOptionList{})), Count: uint32(len(options)), Options: uintptr(unsafe.Pointer(&options[0]))}
		ok, _, callErr := setProxyOption.Call(0, 75, uintptr(unsafe.Pointer(&list)), uintptr(list.Size))
		runtime.KeepAlive(options)
		runtime.KeepAlive(stringsW)
		if ok == 0 {
			return fmt.Errorf("设置 Windows 代理失败（可能受组织策略限制）: %w", callErr)
		}
		for _, option := range []uintptr{39, 37} { // SETTINGS_CHANGED、REFRESH。
			if ok, _, callErr := setProxyOption.Call(0, option, 0, 0); ok == 0 {
				return fmt.Errorf("通知 Windows 代理设置变更失败: %w", callErr)
			}
		}
	}
	return nil
}

func (windowsProxyBackend) Inspect(snapshot ProxySnapshot, endpoint string) SystemProxyStatus {
	if len(snapshot.Entries) != 1 {
		return SystemProxyStatus{State: "unknown", Message: "Windows 代理设置不完整", Supported: true}
	}
	v := snapshot.Entries[0].Values
	flags, _ := strconv.ParseUint(v["flags"], 10, 32)
	server := ""
	if flags&2 != 0 {
		server = v["server"]
	}
	status := windowsProxyStatus(server, flags&(4|8) != 0, endpoint)
	if status.State == "this_app" && strings.Contains(server, "=") {
		for _, part := range strings.Split(server, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && !proxyEndpointMatches(value, endpoint) && (strings.EqualFold(key, "http") || strings.EqualFold(key, "socks")) {
				status.State, status.Message = "other", "未接入 · 原代理存在其他协议地址，不自动接管"
			}
		}
	}
	status.Supported = true
	return status
}

func (windowsProxyBackend) Lock(ctx context.Context) (func(), error) {
	scope, err := windowsProxyScope()
	if err != nil {
		return nil, err
	}
	// 锁名称保留旧版标识，确保升级期间两个名称的进程仍串行操作同一用户的系统代理。
	name, _ := syscall.UTF16PtrFromString(fmt.Sprintf("Global\\ProxyPilot.SystemProxy.%x", sha256.Sum256([]byte(scope))))
	runtime.LockOSThread() // Windows mutex 由线程拥有，释放前不得迁移 goroutine。
	handle, _, callErr := proxyCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("创建系统代理事务锁: %w", callErr)
	}
	for {
		if err := ctx.Err(); err != nil {
			syscall.CloseHandle(syscall.Handle(handle))
			runtime.UnlockOSThread()
			return nil, err
		}
		result, _, waitErr := proxyWaitMutex.Call(handle, 100)
		if result == 0 || result == 0x80 { // 成功或上一持有者异常退出。
			return func() {
				proxyReleaseMutex.Call(handle)
				syscall.CloseHandle(syscall.Handle(handle))
				runtime.UnlockOSThread()
			}, nil
		}
		if result != 0x102 {
			syscall.CloseHandle(syscall.Handle(handle))
			runtime.UnlockOSThread()
			return nil, fmt.Errorf("获取系统代理事务锁: %w", waitErr)
		}
	}
}
