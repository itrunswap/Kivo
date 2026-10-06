//go:build windows

package platform

import (
	"context"
	"testing"
	"unsafe"
)

// 真实 Windows 只读验证 ABI 与 WinInet 查询，不调用 Apply，不改变用户网络。
func TestWindowsSystemProxyCaptureIsReadOnlyAndHasCompleteScope(t *testing.T) {
	if unsafe.Sizeof(winProxyOption{}) != 16 || unsafe.Sizeof(winProxyOptionList{}) != 32 {
		t.Fatal("unsupported WinInet option ABI")
	}
	backend := windowsProxyBackend{}
	snapshot, err := backend.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Backend != "windows-wininet" || snapshot.Scope == "" || len(snapshot.Entries) != 1 {
		t.Fatal("incomplete WinInet capture")
	}
	for _, key := range []string{"flags", "server", "bypass", "pac"} {
		if _, exists := snapshot.Entries[0].Values[key]; !exists {
			t.Fatal("missing field", key)
		}
	}
	target, err := backend.Target(snapshot, "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	if backend.Inspect(target, "127.0.0.1:17890").State != "this_app" {
		t.Fatal("target not recognized")
	}
	actual, err := backend.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !EqualProxySnapshots(snapshot, actual) {
		t.Fatal("read/target construction modified OS or an external app changed it during test")
	}
}
