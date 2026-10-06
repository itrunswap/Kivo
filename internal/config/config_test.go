package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDirAfterRename(t *testing.T) {
	for _, tc := range []struct {
		name    string
		configs []string
		want    string
	}{
		{"fresh install", nil, "Kivo"},
		{"existing Kivo", []string{"Kivo"}, "Kivo"},
		{"existing legacy", []string{"ProxyPilot"}, "ProxyPilot"},
		{"both configurations prefer Kivo", []string{"Kivo", "ProxyPilot"}, "Kivo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			for _, name := range tc.configs {
				root := filepath.Join(base, name)
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := defaultDataDir(base)
			if err != nil || got != filepath.Join(base, tc.want) {
				t.Fatalf("defaultDataDir = %q, %v; want %q", got, err, tc.want)
			}
			// 目录解析只查询，不创建新目录、不复制或删除旧配置。
			if len(tc.configs) == 0 {
				if _, err := os.Stat(got); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("path resolution must not create directories")
				}
			}
		})
	}
}

func TestEmptyNewDirectoryDoesNotHideLegacyConfig(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "Kivo"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(base, "ProxyPilot")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := defaultDataDir(base)
	if err != nil || got != legacy {
		t.Fatalf("empty Kivo directory masked existing configuration: %q, %v", got, err)
	}
}

func TestLoadCreatesSecureDefaultsAndPersistsUpdates(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()
	if cfg.Web.Secret == "" || cfg.Mihomo.ControllerKey == "" {
		t.Fatal("expected generated secrets")
	}
	if cfg.Web.Listen != "127.0.0.1:9099" {
		t.Fatalf("unexpected listen: %s", cfg.Web.Listen)
	}
	if err := store.Update(func(next *Config) error { next.Mihomo.MixedPort = 18080; return nil }); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Snapshot().Mihomo.MixedPort != 18080 {
		t.Fatal("updated port was not persisted")
	}
	if _, err := os.Stat(filepath.Join(root, "config.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("temporary config should not remain")
	}
}

func TestLoadMigratesV1Configuration(t *testing.T) {
	root := t.TempDir()
	paths, _ := ResolvePaths(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := Config{
		SchemaVersion: 1,
		Web:           WebConfig{Listen: "127.0.0.1:9099", Secret: "token"},
		Mihomo:        MihomoConfig{Controller: "127.0.0.1:19090", ControllerKey: "secret", MixedPort: 17890, Mode: "rule"},
		Subscriptions: []Subscription{{Name: "legacy", URL: "https://example.com/sub", Enabled: true}},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(paths.ConfigFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()
	if cfg.SchemaVersion != 2 || cfg.Subscriptions[0].Group != "default" || cfg.Routing.ActiveProfile != "rule" || cfg.Mihomo.DownloadRetry != 4 {
		t.Fatalf("migration result = %#v", cfg)
	}
}

func TestStoreRejectsDuplicateSubscriptions(t *testing.T) {
	paths, _ := ResolvePaths(t.TempDir())
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(next *Config) error {
		next.Subscriptions = []Subscription{{Name: "same", URL: "https://a.example", Enabled: true}, {Name: "same", URL: "https://b.example", Enabled: true}}
		return nil
	})
	if err == nil {
		t.Fatal("expected duplicate subscription validation error")
	}
}

func TestStoreAllowsDisablingWebAuthentication(t *testing.T) {
	paths, _ := ResolvePaths(t.TempDir())
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(next *Config) error {
		next.Web.Secret = ""
		return nil
	}); err != nil {
		t.Fatalf("clearing Web secret should be valid: %v", err)
	}
	if store.Snapshot().Web.Secret != "" {
		t.Fatal("Web secret was not cleared")
	}
	if err := store.Update(func(next *Config) error {
		next.Web.Secret = "abc"
		return nil
	}); err != nil {
		t.Fatalf("short user-defined Web secret should remain compatible: %v", err)
	}
	if _, err := Load(paths); err != nil {
		t.Fatalf("persisted short Web secret should load successfully: %v", err)
	}
}

func TestStoreValidatesAESSubscriptionPassword(t *testing.T) {
	paths, _ := ResolvePaths(t.TempDir())
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	add := func(secret string) error {
		return store.Update(func(next *Config) error {
			next.Subscriptions = []Subscription{{
				Name: "encrypted", URL: "https://example.com/sub", Group: "default", Enabled: true,
				Auth: SubscriptionAuth{Type: "aes", Secret: secret},
			}}
			return nil
		})
	}
	if err := add(""); err == nil {
		t.Fatal("AES subscription without password should be rejected")
	}
	if err := add("compatibility-password"); err != nil {
		t.Fatalf("AES subscription with password should be valid: %v", err)
	}
}

func TestStoreRejectsInvalidSubscriptionUpdateRoute(t *testing.T) {
	paths, _ := ResolvePaths(t.TempDir())
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(next *Config) error {
		next.Subscriptions = []Subscription{{
			Name: "invalid", URL: "https://example.com/sub", Group: "default", Enabled: true, UpdateVia: "sometimes",
		}}
		return nil
	})
	if err == nil {
		t.Fatal("invalid subscription update route should be rejected")
	}
}

func TestProxyLeaseSnapshotAndFailedUpdatesDoNotAlias(t *testing.T) {
	paths, _ := ResolvePaths(t.TempDir())
	store, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(cfg *Config) error {
		cfg.SystemProxy.Lease = &SystemProxyLease{Before: json.RawMessage(`{"before":true}`), Applied: json.RawMessage(`{"applied":true}`), Phase: "active", Endpoint: "127.0.0.1:17890"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	snapshot.SystemProxy.Lease.Before[2] = 'X'
	snapshot.SystemProxy.Lease.Phase = "corrupted"
	actual := store.Snapshot()
	if actual.SystemProxy.Lease.Phase != "active" || string(actual.SystemProxy.Lease.Before) != `{"before":true}` {
		t.Fatal("snapshot aliased live backup")
	}
	if err = store.Update(func(cfg *Config) error { cfg.SystemProxy.Lease.Applied[2] = 'Y'; cfg.Mihomo.MixedPort = 0; return nil }); err == nil {
		t.Fatal("invalid update accepted")
	}
	actual = store.Snapshot()
	if string(actual.SystemProxy.Lease.Applied) != `{"applied":true}` {
		t.Fatal("failed update mutated live backup")
	}
	reloaded, err := Load(paths)
	if err != nil || reloaded.Snapshot().SystemProxy.Lease.Phase != "active" {
		t.Fatal("backup not persisted", err)
	}
}
