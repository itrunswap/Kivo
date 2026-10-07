package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

type rejectingRouteCore struct{ *fakeCore }

func (c *rejectingRouteCore) Restart(context.Context) error {
	return errors.New("invalid mihomo config")
}

type countingModeCore struct {
	*fakeCore
	modeCalls int
}

func (c *countingModeCore) SetMode(context.Context, string) error {
	c.modeCalls++
	return nil
}

func TestBuiltInRoutesCannotBeDeleted(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	for _, name := range []string{"global", "direct", "rule", "bypass-cn", "proxy-only", "bypass-list"} {
		if err := service.DeleteRouteProfile(name); err == nil {
			t.Fatalf("deleted built-in route %s", name)
		}
	}
	for _, name := range []string{"中国大陆直连", "指定地址代理", "指定地址直连"} {
		if err := service.DeleteRuleGroup(name); err == nil {
			t.Fatalf("deleted built-in group %s", name)
		}
	}
	if len(store.Snapshot().Routing.Profiles) != 6 {
		t.Fatal("built-in routes changed")
	}
}

func TestRouteApplyFailureRestoresPersistedRule(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &rejectingRouteCore{fakeCore: &fakeCore{state: core.StateRunning}})
	before := store.Snapshot().Routing.RuleGroups[1].Rules
	err = service.AddRouteRule(context.Background(), "指定地址代理", config.RouteRule{Type: "domain-suffix", Value: "example.com", Action: "proxy"})
	if err == nil || !strings.Contains(err.Error(), "已恢复") {
		t.Fatalf("expected rollback: %v", err)
	}
	if got := store.Snapshot().Routing.RuleGroups[1].Rules; len(got) != len(before) {
		t.Fatalf("invalid rule persisted: %#v", got)
	}
}

func TestRenameSubscriptionGroupUpdatesReferences(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	if err := service.CreateSubscriptionGroup("work"); err != nil {
		t.Fatal(err)
	}
	if err := service.AddSubscription(context.Background(), SubscriptionInput{Name: "one", URL: "https://example.com/sub", Group: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RenameSubscriptionGroup(context.Background(), "work", "office"); err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()
	if cfg.Subscriptions[0].Group != "office" || cfg.SubscriptionGroups[1].Name != "office" {
		t.Fatalf("rename incomplete: %#v %#v", cfg.Subscriptions, cfg.SubscriptionGroups)
	}
	if err := service.RenameSubscriptionGroup(context.Background(), "default", "other"); err == nil {
		t.Fatal("default group renamed")
	}
}

func TestRuleEditMoveAndStaleIndexProtection(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	group := "指定地址代理"
	first := config.RouteRule{Type: "domain", Value: "one.example", Action: "proxy"}
	second := config.RouteRule{Type: "domain", Value: "two.example", Action: "proxy"}
	if err := service.AddRouteRule(context.Background(), group, first); err != nil {
		t.Fatal(err)
	}
	if err := service.AddRouteRule(context.Background(), group, second); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveRouteRuleChecked(context.Background(), group, 1, &second); err == nil {
		t.Fatal("stale rule was deleted")
	}
	changed := config.RouteRule{Type: "domain-suffix", Value: "one.example", Action: "direct"}
	if err := service.UpdateRouteRule(context.Background(), group, 1, first, changed); err != nil {
		t.Fatal(err)
	}
	if err := service.MoveRouteRule(context.Background(), group, 1, 2, changed); err != nil {
		t.Fatal(err)
	}
	rules := store.Snapshot().Routing.RuleGroups[1].Rules
	if len(rules) != 2 || rules[0] != second || rules[1] != changed {
		t.Fatalf("wrong order: %#v", rules)
	}
	if err := service.UpdateRouteRule(context.Background(), group, 1, first, changed); err == nil {
		t.Fatal("stale edit was accepted")
	}
}

func TestInvalidCIDRNeverPersists(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	err = service.AddRouteRule(context.Background(), "指定地址代理", config.RouteRule{Type: "ip-cidr", Value: "not-a-network", Action: "proxy"})
	if err == nil || !strings.Contains(err.Error(), "有效网段") {
		t.Fatalf("missing field error: %v", err)
	}
	if len(store.Snapshot().Routing.RuleGroups[1].Rules) != 0 {
		t.Fatal("invalid rule persisted")
	}
}

func TestRestoreMissingBuiltInRouteWithoutOverwritingCustom(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) error {
		cfg.Routing.Profiles = cfg.Routing.Profiles[1:] // 模拟旧版本允许删除 global。
		cfg.Routing.Profiles = append(cfg.Routing.Profiles, config.RouteProfile{Name: "personal", DefaultAction: "direct"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	if err := service.RestoreRouteDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	profiles := store.Snapshot().Routing.Profiles
	var global, personal bool
	for _, profile := range profiles {
		if profile.Name == "global" {
			global = true
		}
		if profile.Name == "personal" {
			personal = profile.DefaultAction == "direct"
		}
	}
	if !global || !personal {
		t.Fatalf("restore lost profiles: %#v", profiles)
	}
}

func TestMissingPresetDoesNotChangeRunningCoreMode(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) error { cfg.Routing.Profiles = cfg.Routing.Profiles[1:]; return nil }); err != nil {
		t.Fatal(err)
	}
	adapter := &countingModeCore{fakeCore: &fakeCore{state: core.StateRunning}}
	service := NewService(store, adapter)
	if err := service.SetMode(context.Background(), "global"); err == nil {
		t.Fatal("missing profile accepted")
	}
	if adapter.modeCalls != 0 {
		t.Fatal("core mode changed before validation")
	}
}
