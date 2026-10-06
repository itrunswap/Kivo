package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

// 内存平台适配器验证完整事务，但绝不写测试运行者的真实系统代理。
type memorySystemProxy struct {
	current     platform.ProxySnapshot
	writes      int
	applyHook   func(context.Context, platform.ProxySnapshot, int) error
	captureHook func() error
}

func (m *memorySystemProxy) Capture(context.Context) (platform.ProxySnapshot, error) {
	if m.captureHook != nil {
		if err := m.captureHook(); err != nil {
			return platform.ProxySnapshot{}, err
		}
	}
	return platform.CloneProxySnapshot(m.current), nil
}
func (m *memorySystemProxy) Target(before platform.ProxySnapshot, endpoint string) (platform.ProxySnapshot, error) {
	target := platform.CloneProxySnapshot(before)
	target.Entries[0].Values["mode"], target.Entries[0].Values["server"] = "on", endpoint
	target.Entries[0].Values["pac"] = ""
	return target, nil
}
func (m *memorySystemProxy) Disabled(before platform.ProxySnapshot) platform.ProxySnapshot {
	result := platform.CloneProxySnapshot(before)
	result.Entries[0].Values["mode"] = "off"
	return result
}
func (m *memorySystemProxy) Apply(ctx context.Context, target platform.ProxySnapshot) error {
	m.writes++
	if m.applyHook != nil {
		if err := m.applyHook(ctx, target, m.writes); err != nil {
			return err
		}
	}
	m.current = platform.CloneProxySnapshot(target)
	return nil
}
func (m *memorySystemProxy) Inspect(snapshot platform.ProxySnapshot, endpoint string) platform.SystemProxyStatus {
	v := snapshot.Entries[0].Values
	state := "off"
	if v["mode"] == "auto" {
		state = "automatic"
	} else if v["mode"] == "on" {
		state = "other"
		if v["server"] == endpoint {
			state = "this_app"
		}
	}
	return platform.SystemProxyStatus{State: state, Message: state, Supported: true}
}
func (m *memorySystemProxy) Lock(context.Context) (func(), error) { return func() {}, nil }

func listenProxyFixture(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connection.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func newProxyService(t *testing.T) (*Service, *config.Store, *fakeCore, *memorySystemProxy) {
	t.Helper()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	port := listenProxyFixture(t)
	if err = store.Update(func(cfg *config.Config) error { cfg.Mihomo.MixedPort = port; cfg.Mihomo.AutoStart = false; return nil }); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateStopped}
	backend := &memorySystemProxy{current: platform.ProxySnapshot{Backend: "memory", Scope: "test-user", Entries: []platform.ProxyEntry{{Name: "current", Values: map[string]string{"mode": "off", "server": "original:80", "pac": "https://original/private?secret=hidden", "bypass": "local"}}}}}
	return NewServiceWithSystemProxy(store, adapter, backend), store, adapter, backend
}

func TestConnectDisconnectJournalAndIdempotence(t *testing.T) {
	s, store, adapter, backend := newProxyService(t)
	before := platform.CloneProxySnapshot(backend.current)
	backend.applyHook = func(_ context.Context, _ platform.ProxySnapshot, write int) error {
		if write == 1 {
			lease := store.Snapshot().SystemProxy.Lease
			if lease == nil || lease.Phase != "prepared" {
				t.Fatal("OS changed before durable journal")
			}
			disk, err := os.ReadFile(store.Paths().ConfigFile)
			if err != nil || !json.Valid(disk) {
				t.Fatal("journal not saved", err)
			}
		}
		return nil
	}
	ctx := context.Background()
	result, err := s.Connect(ctx, ConnectOptions{SkipCheck: true})
	if err != nil || !result.SystemProxy.Managed || adapter.starts != 1 {
		t.Fatal(result, err, adapter)
	}
	lease := store.Snapshot().SystemProxy.Lease
	original := string(lease.Before)
	if _, err = s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	if backend.writes != 1 || adapter.starts != 1 || string(store.Snapshot().SystemProxy.Lease.Before) != original {
		t.Fatal("duplicate connect changed baseline")
	}
	statusJSON, _ := json.Marshal(s.SystemProxyStatus(ctx))
	if string(statusJSON) == "" || containsSecret(string(statusJSON)) {
		t.Fatal("private snapshot leaked", string(statusJSON))
	}
	result, err = s.Disconnect(ctx)
	if err != nil || result.Warning || adapter.stops != 1 || !platform.EqualProxySnapshots(before, backend.current) {
		t.Fatal(result, err)
	}
	cfg := store.Snapshot()
	if cfg.SystemProxy.Lease != nil || cfg.SystemProxy.AutoConnect || cfg.Mihomo.AutoStart {
		t.Fatal("disconnect preference not cleared")
	}
}

func containsSecret(value string) bool {
	for _, secret := range []string{"original/private", "secret=hidden", "original:80"} {
		for i := 0; i+len(secret) <= len(value); i++ {
			if value[i:i+len(secret)] == secret {
				return true
			}
		}
	}
	return false
}

func TestConnectRequiresExplicitReplaceOrAdopt(t *testing.T) {
	for _, state := range []string{"other", "automatic", "this_app"} {
		t.Run(state, func(t *testing.T) {
			s, store, adapter, backend := newProxyService(t)
			v := backend.current.Entries[0].Values
			v["mode"] = "on"
			v["server"] = "company:88"
			if state == "automatic" {
				v["mode"] = "auto"
			}
			if state == "this_app" {
				v["server"] = "127.0.0.1:" + strconv.Itoa(store.Snapshot().Mihomo.MixedPort)
			}
			before := platform.CloneProxySnapshot(backend.current)
			if _, err := s.Connect(context.Background(), ConnectOptions{SkipCheck: true}); err == nil {
				t.Fatal("implicit overwrite")
			}
			if backend.writes != 0 || store.Snapshot().SystemProxy.Lease != nil || adapter.state != core.StateStopped {
				t.Fatal("rejected connect changed settings/core")
			}
			options := ConnectOptions{SkipCheck: true, Replace: state != "this_app", Adopt: state == "this_app"}
			if _, err := s.Connect(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Disconnect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if state == "this_app" {
				before = s.systemProxy.Disabled(before)
			}
			if !platform.EqualProxySnapshots(backend.current, before) {
				t.Fatal(backend.current, before)
			}
		})
	}
}

func TestConnectRollbackPartialApplicationAndCancellation(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelRequest), func(t *testing.T) {
			s, store, adapter, backend := newProxyService(t)
			before := platform.CloneProxySnapshot(backend.current)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend.applyHook = func(cleanup context.Context, target platform.ProxySnapshot, write int) error {
				if write == 1 {
					backend.current.Entries[0].Values["server"] = target.Entries[0].Values["server"]
					if cancelRequest {
						cancel()
					}
					return errors.New("simulated partial system write")
				}
				if cleanup.Err() != nil {
					t.Fatal("rollback reused cancelled request")
				}
				return nil
			}
			if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err == nil {
				t.Fatal("failed OS apply treated as success")
			}
			if !platform.EqualProxySnapshots(before, backend.current) || store.Snapshot().SystemProxy.Lease != nil || adapter.state != core.StateStopped || store.Snapshot().Mihomo.AutoStart {
				t.Fatal("rollback incomplete", backend.current)
			}
		})
	}
}

func TestRestorePreservesExternalChangesAndForceOnlyOwnedFields(t *testing.T) {
	s, store, adapter, backend := newProxyService(t)
	before := platform.CloneProxySnapshot(backend.current)
	ctx := context.Background()
	if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	backend.current.Entries[0].Values["server"] = "external:99"
	backend.current.Entries[0].Values["bypass"] = "external-bypass"
	result, err := s.Disconnect(ctx)
	if err != nil || !result.Warning || !result.SystemProxy.Conflict || store.Snapshot().SystemProxy.Lease == nil || adapter.state != core.StateStopped || backend.current.Entries[0].Values["server"] != "external:99" {
		t.Fatal(result, err)
	}
	if _, err = s.Connect(ctx, ConnectOptions{Replace: true, SkipCheck: true}); err == nil {
		t.Fatal("connect overwrote pending conflicting backup")
	}
	result, err = s.RestoreSystemProxy(ctx, true)
	if err != nil || result.Warning || store.Snapshot().SystemProxy.Lease != nil {
		t.Fatal(result, err)
	}
	before.Entries[0].Values["bypass"] = "external-bypass"
	if !platform.EqualProxySnapshots(before, backend.current) {
		t.Fatal("force altered unowned field", backend.current, before)
	}
}

func TestRestoreUnownedChangesDoesNotConflict(t *testing.T) {
	s, _, _, backend := newProxyService(t)
	ctx := context.Background()
	if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	backend.current.Entries[0].Values["bypass"] = "new bypass"
	result, err := s.RestoreSystemProxy(ctx, false)
	if err != nil || result.Warning || backend.current.Entries[0].Values["bypass"] != "new bypass" {
		t.Fatal(result, err)
	}
}

func TestFailedRestoreDoesNotStopCoreOrDropBackup(t *testing.T) {
	s, store, adapter, backend := newProxyService(t)
	ctx := context.Background()
	if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	backend.applyHook = func(context.Context, platform.ProxySnapshot, int) error { return errors.New("permission denied") }
	if err := s.StopCore(ctx); err == nil {
		t.Fatal("stop ignored failed restore")
	}
	if adapter.stops != 0 || store.Snapshot().SystemProxy.Lease == nil {
		t.Fatal("unsafe stop or backup deletion")
	}
	if err := s.SafeShutdown(ctx); err == nil || adapter.stops != 0 {
		t.Fatal("shutdown ignored failed restore", err)
	}
}

func TestSafeShutdownPreservesPreferencesAndResume(t *testing.T) {
	s, store, adapter, backend := newProxyService(t)
	ctx := context.Background()
	original := platform.CloneProxySnapshot(backend.current)
	if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SafeShutdown(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()
	if !cfg.SystemProxy.AutoConnect || !cfg.Mihomo.AutoStart || cfg.SystemProxy.Lease != nil || !platform.EqualProxySnapshots(original, backend.current) {
		t.Fatal("shutdown lost preferences or restoration")
	}
	if err := s.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if adapter.state != core.StateRunning || !s.SystemProxyStatus(ctx).Managed {
		t.Fatal("resume failed")
	}
	// 模拟异常结束：已持久化的接管仍在，下一次启动先恢复原设置，再按偏好连接。
	adapter.state = core.StateStopped
	if err := s.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.SystemProxyStatus(ctx).Managed {
		t.Fatal("crash recovery failed")
	}
	_, _ = s.Disconnect(ctx)
}

func TestResumeDoesNotReplaceNewExternalProxy(t *testing.T) {
	s, store, _, backend := newProxyService(t)
	ctx := context.Background()
	if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SafeShutdown(ctx); err != nil {
		t.Fatal(err)
	}
	backend.current.Entries[0].Values["mode"], backend.current.Entries[0].Values["server"] = "on", "new-app:80"
	writes := backend.writes
	if err := s.Resume(ctx); err == nil {
		t.Fatal("resume silently replaced another app")
	}
	if writes != backend.writes || store.Snapshot().SystemProxy.Lease != nil {
		t.Fatal("resume altered external settings")
	}
}

func TestOfflineRestoreAndCorruptOrWrongScopeJournal(t *testing.T) {
	for _, scenario := range []string{"offline", "corrupt", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, _, backend := newProxyService(t)
			ctx := context.Background()
			if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
				t.Fatal(err)
			}
			if scenario == "corrupt" {
				_ = store.Update(func(cfg *config.Config) error { cfg.SystemProxy.Lease.Before = json.RawMessage(`{}`); return nil })
			}
			if scenario == "scope" {
				backend.current.Scope = "other-user"
			}
			offline := NewServiceWithSystemProxy(store, nil, backend)
			writes := backend.writes
			_, err := offline.RestoreSystemProxy(ctx, true)
			if scenario == "offline" {
				if err != nil || store.Snapshot().SystemProxy.Lease != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || backend.writes != writes || store.Snapshot().SystemProxy.Lease == nil {
				t.Fatal("invalid backup not protected", err)
			}
		})
	}
}

func TestConnectionMutexRejectsConcurrentMutations(t *testing.T) {
	s, _, _, backend := newProxyService(t)
	s.subscriptionAction.Lock()
	defer s.subscriptionAction.Unlock()
	for _, operation := range []func() error{
		func() error { _, err := s.Connect(context.Background(), ConnectOptions{SkipCheck: true}); return err },
		func() error { _, err := s.Disconnect(context.Background()); return err },
		func() error { _, err := s.RestoreSystemProxy(context.Background(), false); return err },
		func() error { return s.UpdateSettings(context.Background(), s.GetSettings()) },
		func() error { return s.AddSubscription(context.Background(), SubscriptionInput{}) },
	} {
		if err := operation(); err == nil {
			t.Fatal("concurrent mutation allowed")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.SafeShutdown(ctx); err == nil || backend.writes != 0 {
		t.Fatal("shutdown did not respect busy deadline")
	}
}

func TestNoListeningPortDoesNotChangeSystemProxy(t *testing.T) {
	s, store, _, backend := newProxyService(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	_ = store.Update(func(cfg *config.Config) error { cfg.Mihomo.MixedPort = port; return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err = s.Connect(ctx, ConnectOptions{SkipCheck: true}); err == nil || backend.writes != 0 || store.Snapshot().SystemProxy.Lease != nil {
		t.Fatal("dead port connected", err)
	}
}

func TestManagedProxyFollowsPortChange(t *testing.T) {
	s, store, _, backend := newProxyService(t)
	ctx := context.Background()
	if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	settings := s.GetSettings()
	settings.MixedPort = listenProxyFixture(t)
	if err := s.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if backend.current.Entries[0].Values["server"] != "127.0.0.1:"+strconv.Itoa(settings.MixedPort) || !s.SystemProxyStatus(ctx).Managed || !store.Snapshot().SystemProxy.AutoConnect {
		t.Fatal("proxy still points to old port")
	}
	_, _ = s.Disconnect(ctx)
}

func TestFailedJournalSaveNeverChangesSystem(t *testing.T) {
	s, store, adapter, backend := newProxyService(t)
	// 仅测试临时目录：把预期临时文件路径占用为目录，模拟无法写备份。
	if err := os.Mkdir(store.Paths().ConfigFile+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(context.Background(), ConnectOptions{SkipCheck: true}); err == nil {
		t.Fatal("unpersisted journal accepted")
	}
	if backend.writes != 0 || store.Snapshot().SystemProxy.Lease != nil || adapter.state != core.StateStopped {
		t.Fatal("mutated system without backup")
	}
}

func TestConnectWithFailedVerificationRetainsBackupAndLiveCore(t *testing.T) {
	s, store, adapter, backend := newProxyService(t)
	backend.applyHook = func(_ context.Context, target platform.ProxySnapshot, write int) error {
		if write == 1 {
			backend.current.Entries[0].Values["server"] = target.Entries[0].Values["server"]
			return errors.New("partial write")
		}
		return errors.New("restore refused")
	}
	if _, err := s.Connect(context.Background(), ConnectOptions{SkipCheck: true}); err == nil {
		t.Fatal("failed connection accepted")
	}
	if store.Snapshot().SystemProxy.Lease == nil || adapter.stops != 0 || adapter.state != core.StateRunning {
		t.Fatal("dead port or lost backup after failed rollback")
	}
}

func TestConnectNetworkFailureIsWarningNotFalseSuccessOrRollback(t *testing.T) {
	s, store, adapter, _ := newProxyService(t)
	// 三条路径均为回环 fixture，绝不访问外网。测试失败不会撤销有效的系统接入。
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer failed.Close()
	s.probeTargets = []probeTarget{{name: "fixture", address: failed.URL, status: 204}}
	port := failed.Listener.Addr().(*net.TCPAddr).Port
	_ = store.Update(func(cfg *config.Config) error { cfg.Mihomo.MixedPort = port; return nil })
	result, err := s.Connect(context.Background(), ConnectOptions{})
	if err != nil || !result.Warning || result.Connectivity == nil || !result.SystemProxy.Managed || adapter.state != core.StateRunning || store.Snapshot().SystemProxy.Lease == nil {
		t.Fatal(result, err)
	}
	if result.Connectivity.Routes[1].State != "failed" {
		t.Fatal("probe failed but reported success")
	}
	_, _ = s.Disconnect(context.Background())
}

type failingRestartCore struct {
	*fakeCore
	keepAlive bool
}

func (f *failingRestartCore) Restart(context.Context) error {
	if !f.keepAlive {
		f.state = core.StateFailed
	}
	return errors.New("restart/download failed")
}

func TestCoreMutationFailureOnlyRestoresWhenPortIsLost(t *testing.T) {
	for _, keepAlive := range []bool{true, false} {
		t.Run(strconv.FormatBool(keepAlive), func(t *testing.T) {
			s, store, adapter, backend := newProxyService(t)
			ctx := context.Background()
			before := platform.CloneProxySnapshot(backend.current)
			if _, err := s.Connect(ctx, ConnectOptions{SkipCheck: true}); err != nil {
				t.Fatal(err)
			}
			s.core = &failingRestartCore{fakeCore: adapter, keepAlive: keepAlive}
			if err := s.RestartCore(ctx); err == nil {
				t.Fatal("restart failure hidden")
			}
			if keepAlive {
				if store.Snapshot().SystemProxy.Lease == nil || !s.SystemProxyStatus(ctx).Managed {
					t.Fatal("live proxy unnecessarily disconnected")
				}
				_, _ = s.Disconnect(ctx)
			} else {
				if store.Snapshot().SystemProxy.Lease != nil || store.Snapshot().SystemProxy.AutoConnect || !platform.EqualProxySnapshots(before, backend.current) {
					t.Fatal("dead proxy not restored")
				}
			}
		})
	}
}
