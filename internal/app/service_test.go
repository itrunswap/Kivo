package app

import (
	"context"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

func TestPublicSubscriptionURLsHidePathCredentials(t *testing.T) {
	for _, raw := range []string{"https://example.com/link/super-secret-subscription-token?sub=3", "https://example.com/s/short-secret", "https://example.com/subscribe/short-secret#private", "https://user:password@example.com/api/012345678901234567890123?token=sensitive"} {
		public := redactURL(raw)
		for _, secret := range []string{"super-secret-subscription-token", "short-secret", "private", "password", "012345678901234567890123", "sensitive"} {
			if strings.Contains(public, secret) {
				t.Fatalf("credential exposed: %s", public)
			}
		}
	}
}

type fakeCore struct {
	state    core.State
	starts   int
	stops    int
	restarts int
	updated  []string
	tested   []string
}

func (f *fakeCore) Install(context.Context, string, func(core.InstallEvent)) error { return nil }
func (f *fakeCore) Start(context.Context) error {
	f.starts++
	f.state = core.StateRunning
	return nil
}
func (f *fakeCore) Stop(context.Context) error {
	f.stops++
	f.state = core.StateStopped
	return nil
}
func (f *fakeCore) Restart(context.Context) error                     { f.restarts++; return nil }
func (f *fakeCore) Status(context.Context) core.Status                { return core.Status{State: f.state} }
func (f *fakeCore) ListNodes(context.Context) ([]core.Node, error)    { return nil, nil }
func (f *fakeCore) TestNodes(context.Context) (map[string]int, error) { return nil, nil }
func (f *fakeCore) SelectNode(context.Context, string) error          { return nil }
func (f *fakeCore) UpdateSubscription(_ context.Context, name string) error {
	f.updated = append(f.updated, name)
	return nil
}
func (f *fakeCore) TestSubscription(_ context.Context, name string) error {
	f.tested = append(f.tested, name)
	return nil
}
func (f *fakeCore) SetMode(context.Context, string) error { return nil }
func (f *fakeCore) Logs(int) []string                     { return nil }

func TestAddSubscriptionRedactsSecretsAndRestartsRunningCore(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateRunning}
	service := NewService(store, adapter)
	err = service.AddSubscription(context.Background(), SubscriptionInput{Name: "测试", URL: "https://example.com/sub?token=sensitive", AuthType: "bearer", Secret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.restarts != 1 {
		t.Fatalf("expected one restart, got %d", adapter.restarts)
	}
	items := service.ListSubscriptions()
	if len(items) != 1 || items[0].URL != "https://example.com/sub?token=%2A%2A%2A" {
		t.Fatalf("URL was not redacted: %#v", items)
	}
}

func TestUpdateWebTokenTakesEffectAndCanDisableAuthentication(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	security, err := service.UpdateWebToken("a-secure-token-value")
	if err != nil || !security.AuthEnabled {
		t.Fatalf("enabling token = %#v, %v", security, err)
	}
	if store.Snapshot().Web.Secret != "a-secure-token-value" {
		t.Fatal("new Web token was not persisted")
	}
	security, err = service.UpdateWebToken("")
	if err != nil || security.AuthEnabled {
		t.Fatalf("disabling token = %#v, %v", security, err)
	}
	security, err = service.UpdateWebToken("abc")
	if err != nil || !security.AuthEnabled || store.Snapshot().Web.Secret != "abc" {
		t.Fatalf("short token should be accepted: %#v, %v", security, err)
	}
}

func TestDisablingSubscriptionAuthenticationClearsStoredCredentials(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "encrypted", URL: "https://example.com/sub", AuthType: "aes", Secret: "sensitive-password",
	}); err != nil {
		t.Fatal(err)
	}
	none := "none"
	if err := service.PatchSubscription(context.Background(), SubscriptionPatch{Reference: "encrypted", AuthType: &none}); err != nil {
		t.Fatal(err)
	}
	auth := store.Snapshot().Subscriptions[0].Auth
	if auth.Type != "none" || auth.Secret != "" || auth.Username != "" {
		t.Fatalf("credentials should be cleared when auth is disabled: %#v", auth)
	}
}

func TestSubscriptionGroupsAndRoutingRulesReloadRunningCore(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateRunning}
	service := NewService(store, adapter)
	if err := service.CreateSubscriptionGroup("work"); err != nil {
		t.Fatal(err)
	}
	if err := service.AddSubscription(context.Background(), SubscriptionInput{URL: "https://work.example/sub", Group: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSubscriptionGroup(context.Background(), "work", true, true); err != nil {
		t.Fatal(err)
	}
	groups := service.SubscriptionGroups()
	if groups[0].Enabled || !groups[1].Enabled {
		t.Fatalf("exclusive group state = %#v", groups)
	}
	if err := service.CreateRuleGroup("custom"); err != nil {
		t.Fatal(err)
	}
	if err := service.AddRouteRule(context.Background(), "custom", config.RouteRule{Type: "domain-suffix", Value: "github.com", Action: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateRouteProfile("work-only", "direct", []string{"custom"}); err != nil {
		t.Fatal(err)
	}
	if err := service.UseRouteProfile(context.Background(), "work-only"); err != nil {
		t.Fatal(err)
	}
	if adapter.restarts < 3 {
		t.Fatalf("running core should reload after changes, restarts = %d", adapter.restarts)
	}
}

func TestUpdateSettingsKeepsModeAndRoutingProfileConsistent(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	settings := service.GetSettings()
	settings.Mode = "global"
	if err := service.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Routing.ActiveProfile; got != "global" {
		t.Fatalf("active profile = %q, want global", got)
	}
	settings.Mode = "rule"
	if err := service.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Routing.ActiveProfile; got != "rule" {
		t.Fatalf("active profile = %q, want rule", got)
	}
}

func TestAllProviderActionsSkipDisabledSubscriptionGroups(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{}
	service := NewService(store, adapter)
	if err := service.CreateSubscriptionGroup("disabled"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSubscriptionGroup(context.Background(), "disabled", false, false); err != nil {
		t.Fatal(err)
	}
	if err := service.AddSubscription(context.Background(), SubscriptionInput{Name: "active", URL: "https://active.example/sub"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AddSubscription(context.Background(), SubscriptionInput{Name: "inactive", URL: "https://inactive.example/sub", Group: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSubscriptionReference(context.Background(), "all"); err != nil {
		t.Fatal(err)
	}
	if err := service.TestSubscriptionReference(context.Background(), "all"); err != nil {
		t.Fatal(err)
	}
	if len(adapter.updated) != 2 || adapter.updated[0] != "active" || adapter.updated[1] != "active" || len(adapter.tested) != 1 || adapter.tested[0] != "active" {
		t.Fatalf("provider actions updated=%v tested=%v", adapter.updated, adapter.tested)
	}
}

func TestUpdateSubscriptionGroupViaProxyTemporarilyStartsStoppedCore(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateStopped}
	service := NewService(store, adapter)
	if err := service.CreateSubscriptionGroup("work"); err != nil {
		t.Fatal(err)
	}
	if err := service.AddSubscription(context.Background(), SubscriptionInput{Name: "default-sub", URL: "https://default.example/sub"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AddSubscription(context.Background(), SubscriptionInput{Name: "work-sub", URL: "https://work.example/sub", Group: "work"}); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{Group: "work", Via: "proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TemporarilyStartedCore || adapter.starts != 1 || adapter.stops != 1 || adapter.state != core.StateStopped {
		t.Fatalf("temporary lifecycle result=%#v starts=%d stops=%d state=%s", result, adapter.starts, adapter.stops, adapter.state)
	}
	if len(adapter.updated) != 1 || adapter.updated[0] != "work-sub" {
		t.Fatalf("updated subscriptions = %v", adapter.updated)
	}
	cfg := store.Snapshot()
	if cfg.Subscriptions[0].UpdateVia != "direct" || cfg.Subscriptions[1].UpdateVia != "proxy" {
		t.Fatalf("unexpected update routes: %#v", cfg.Subscriptions)
	}
}

func TestChangingUpdateRouteRestartsRunningCoreOnce(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateRunning}
	service := NewService(store, adapter)
	if err := service.AddSubscription(context.Background(), SubscriptionInput{Name: "primary", URL: "https://example.com/sub"}); err != nil {
		t.Fatal(err)
	}
	// AddSubscription 会为运行中的内核执行一次重载；这里只统计更新路径变更产生的重启。
	adapter.restarts = 0
	result, err := service.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{Reference: "primary", Via: "proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TemporarilyStartedCore || adapter.restarts != 1 || adapter.starts != 0 || adapter.stops != 0 {
		t.Fatalf("result=%#v restarts=%d starts=%d stops=%d", result, adapter.restarts, adapter.starts, adapter.stops)
	}
}

func TestExplicitUnchangedUpdateRouteDoesNotRestart(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateRunning}
	service := NewService(store, adapter)
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "primary", URL: "https://example.com/sub", UpdateVia: "direct",
	}); err != nil {
		t.Fatal(err)
	}
	adapter.restarts = 0
	if _, err := service.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{
		Reference: "primary", Via: "direct",
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.restarts != 0 {
		t.Fatalf("unchanged route must not restart, got %d", adapter.restarts)
	}
}

func TestSubscriptionCheckViaDirectTemporarilyStartsStoppedCore(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeCore{state: core.StateStopped}
	service := NewService(store, adapter)
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "primary", URL: "https://example.com/sub", UpdateVia: "proxy",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := service.TestSubscriptions(context.Background(), SubscriptionTestInput{
		Reference: "primary", Via: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TemporarilyStartedCore || adapter.starts != 1 || adapter.stops != 1 || adapter.state != core.StateStopped {
		t.Fatalf("result=%#v starts=%d stops=%d state=%s", result, adapter.starts, adapter.stops, adapter.state)
	}
	if len(result.Tested) != 1 || result.Tested[0] != "primary" || len(adapter.tested) != 1 || len(adapter.updated) != 1 {
		t.Fatalf("tested result=%#v checked=%v refreshed=%v", result, adapter.tested, adapter.updated)
	}
	if got := store.Snapshot().Subscriptions[0].UpdateVia; got != "direct" {
		t.Fatalf("saved check route=%q, want direct", got)
	}
}
