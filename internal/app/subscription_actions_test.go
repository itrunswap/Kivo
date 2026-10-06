package app

import (
	"context"
	"errors"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

type reportingCore struct {
	*fakeCore
	reloads   int
	reloadErr error
}

func (c *reportingCore) Reload(context.Context) error { c.reloads++; return c.reloadErr }
func (c *reportingCore) UpdateSubscription(ctx context.Context, name string) error {
	if name == "bad" {
		return errors.New("upstream unavailable")
	}
	return c.fakeCore.UpdateSubscription(ctx, name)
}
func (c *reportingCore) SubscriptionSnapshot(_ context.Context, name string) (core.ProviderSnapshot, error) {
	count := 2
	for _, updated := range c.updated {
		if updated == name {
			count = 3
		}
	}
	return core.ProviderSnapshot{Nodes: make([]core.Node, count)}, nil
}
func actionTestService(t *testing.T, auth string) (*Service, *reportingCore) {
	t.Helper()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Subscriptions = []config.Subscription{{Name: "good", URL: "https://example.com/sub", Auth: config.SubscriptionAuth{Type: auth, Secret: "test"}, Enabled: true, Group: "default", UpdateVia: "direct"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &reportingCore{fakeCore: &fakeCore{state: core.StateRunning}}
	return NewService(store, adapter), adapter
}
func TestSubscriptionRouteReloadPolicy(t *testing.T) {
	for _, auth := range []string{"none", "aes"} {
		t.Run(auth, func(t *testing.T) {
			s, c := actionTestService(t, auth)
			for _, via := range []string{"direct", "proxy", "proxy", "direct"} {
				if _, err := s.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{Via: via}); err != nil {
					t.Fatal(err)
				}
			}
			want := 2
			if auth == "aes" {
				want = 0
			}
			if c.reloads != want || c.restarts != 0 {
				t.Fatalf("reloads=%d restarts=%d", c.reloads, c.restarts)
			}
		})
	}
}
func TestSubscriptionRouteRollbackOnReloadFailure(t *testing.T) {
	s, c := actionTestService(t, "none")
	c.reloadErr = errors.New("reload failed")
	if _, err := s.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{Via: "proxy"}); err == nil {
		t.Fatal("expected failure")
	}
	if s.store.Snapshot().Subscriptions[0].UpdateVia != "direct" {
		t.Fatal("route not rolled back")
	}
	if len(c.updated) != 0 {
		t.Fatal("must not update using unapplied route")
	}
}
func TestSubscriptionPartialResultsRetainCounts(t *testing.T) {
	s, c := actionTestService(t, "none")
	s.store.Update(func(cfg *config.Config) error {
		bad := cfg.Subscriptions[0]
		bad.Name = "bad"
		cfg.Subscriptions = append(cfg.Subscriptions, bad)
		return nil
	})
	result, err := s.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{})
	if err == nil || len(result.Results) != 2 || len(result.Updated) != 1 || len(c.updated) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	good := result.Results[0]
	if good.NodeCount == nil || *good.NodeCount != 3 || good.PreviousCount == nil || *good.PreviousCount != 2 {
		t.Fatalf("bad counts: %+v", good)
	}
	if result.Results[1].NodeCount != nil || result.Results[1].Error == "" {
		t.Fatal("failure must not fabricate zero count")
	}
}
func TestSubscriptionConcurrentLifecycleRejected(t *testing.T) {
	s, c := actionTestService(t, "aes")
	s.subscriptionAction.Lock()
	defer s.subscriptionAction.Unlock()
	if _, err := s.UpdateSubscriptions(context.Background(), SubscriptionUpdateInput{}); err == nil {
		t.Fatal("concurrent update allowed")
	}
	if _, err := s.TestSubscriptions(context.Background(), SubscriptionTestInput{}); err == nil {
		t.Fatal("concurrent test allowed")
	}
	if err := s.StopCore(context.Background()); err == nil || c.stops != 0 {
		t.Fatal("temporary core can be stopped concurrently")
	}
}
