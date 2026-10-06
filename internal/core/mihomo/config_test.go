package mihomo

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	appconfig "github.com/itrunswap/Kivo/internal/config"
)

func TestGenerateConfigWithAuthenticatedProvider(t *testing.T) {
	cfg := appconfig.Config{
		SchemaVersion: 1,
		Mihomo:        appconfig.MihomoConfig{Controller: "127.0.0.1:19090", ControllerKey: "secret", MixedPort: 17890, Mode: "rule", TUNEnabled: true},
		Subscriptions: []appconfig.Subscription{{
			Name: "主线路", URL: "https://example.com/sub", Enabled: true,
			Auth:           appconfig.SubscriptionAuth{Type: "basic", Username: "alice", Secret: "password"},
			UpdateInterval: 3600, HealthInterval: 300,
		}},
	}
	path, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("generated config is not JSON: %v", err)
	}
	if result["mixed-port"].(float64) != 17890 {
		t.Fatal("mixed port missing")
	}
	providers := result["proxy-providers"].(map[string]any)
	provider := providers["主线路"].(map[string]any)
	headers := provider["header"].(map[string]any)
	if userAgent := headers["User-Agent"].([]any)[0].(string); userAgent != "Clash.Meta" {
		t.Fatalf("unexpected provider User-Agent: %s", userAgent)
	}
	auth := headers["Authorization"].([]any)[0].(string)
	if auth != "Basic YWxpY2U6cGFzc3dvcmQ=" {
		t.Fatalf("unexpected basic auth: %s", auth)
	}
	if _, ok := result["tun"]; !ok {
		t.Fatal("TUN config missing")
	}
}

func TestGenerateConfigRoutesAESProviderThroughProtectedLocalAdapter(t *testing.T) {
	cfg := appconfig.Config{
		SchemaVersion: 2,
		Web:           appconfig.WebConfig{Listen: "0.0.0.0:9099"},
		Mihomo:        appconfig.MihomoConfig{Controller: "127.0.0.1:19090", ControllerKey: "internal-secret", MixedPort: 17890, Mode: "rule"},
		Subscriptions: []appconfig.Subscription{{
			Name: "加密 线路", URL: "https://example.com/private?token=must-not-leak", Group: "default", Enabled: true,
			Auth: appconfig.SubscriptionAuth{Type: "aes", Secret: "password"},
		}},
		SubscriptionGroups: []appconfig.SubscriptionGroup{{Name: "default", Enabled: true}},
	}
	path, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-not-leak") || strings.Contains(string(data), "\"password\"") {
		t.Fatal("generated Mihomo config leaked the source URL or AES password")
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	provider := result["proxy-providers"].(map[string]any)["加密 线路"].(map[string]any)
	if provider["proxy"] != "DIRECT" {
		t.Fatalf("AES local adapter must be reached directly: %v", provider["proxy"])
	}
	if got := provider["url"].(string); got != "http://127.0.0.1:9099/api/v1/internal/subscription-content?name=%E5%8A%A0%E5%AF%86+%E7%BA%BF%E8%B7%AF" {
		t.Fatalf("unexpected local adapter URL: %s", got)
	}
	headers := provider["header"].(map[string]any)
	if got := headers["X-Kivo-Internal"].([]any)[0]; got != "internal-secret" {
		t.Fatalf("unexpected internal key: %v", got)
	}
}

func TestGenerateConfigUsesProxyGroupForProviderUpdates(t *testing.T) {
	cfg := appconfig.Config{
		Web:    appconfig.WebConfig{Listen: "127.0.0.1:9099"},
		Mihomo: appconfig.MihomoConfig{Controller: "127.0.0.1:19090", ControllerKey: "secret", MixedPort: 17890, Mode: "rule"},
		Subscriptions: []appconfig.Subscription{{
			Name: "proxied", URL: "https://example.com/sub", Enabled: true, UpdateVia: "proxy",
		}},
	}
	path, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	provider := result["proxy-providers"].(map[string]any)["proxied"].(map[string]any)
	if provider["proxy"] != "PROXY" {
		t.Fatalf("provider update proxy = %v, want PROXY", provider["proxy"])
	}
}

func TestGenerateConfigRejectsAESWithoutInternalKey(t *testing.T) {
	cfg := appconfig.Config{
		Web:    appconfig.WebConfig{Listen: "127.0.0.1:9099"},
		Mihomo: appconfig.MihomoConfig{MixedPort: 17890, Mode: "rule"},
		Subscriptions: []appconfig.Subscription{{
			Name: "encrypted", URL: "https://example.com/sub", Enabled: true,
			Auth: appconfig.SubscriptionAuth{Type: "aes", Secret: "password"},
		}},
	}
	if _, err := GenerateConfig(cfg, t.TempDir()); err == nil {
		t.Fatal("AES provider without internal key should be rejected")
	}
}

func TestGenerateConfigWithoutProvidersUsesDirect(t *testing.T) {
	cfg := appconfig.Config{SchemaVersion: 1, Mihomo: appconfig.MihomoConfig{Controller: "127.0.0.1:19090", ControllerKey: "secret", MixedPort: 17890, Mode: "rule"}}
	path, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	groups := result["proxy-groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected one direct group, got %d", len(groups))
	}
}

func TestGenerateConfigUsesEnabledSubscriptionGroupsAndOrderedRules(t *testing.T) {
	cfg := appconfig.Config{
		SchemaVersion:      2,
		Mihomo:             appconfig.MihomoConfig{Controller: "127.0.0.1:19090", ControllerKey: "secret", MixedPort: 17890, Mode: "rule"},
		SubscriptionGroups: []appconfig.SubscriptionGroup{{Name: "daily", Enabled: true}, {Name: "work", Enabled: false}},
		Subscriptions: []appconfig.Subscription{
			{Name: "daily-provider", URL: "https://daily.example/sub", Group: "daily", Enabled: true},
			{Name: "work-provider", URL: "https://work.example/sub", Group: "work", Enabled: true},
		},
		Routing: appconfig.RoutingConfig{
			ActiveProfile: "custom",
			Profiles:      []appconfig.RouteProfile{{Name: "custom", DefaultAction: "direct", Groups: []string{"work-rules"}}},
			RuleGroups:    []appconfig.RuleGroup{{Name: "work-rules", Rules: []appconfig.RouteRule{{Type: "domain-suffix", Value: "github.com", Action: "proxy"}}}},
		},
	}
	path, err := GenerateConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	providers := result["proxy-providers"].(map[string]any)
	if _, ok := providers["daily-provider"]; !ok {
		t.Fatal("enabled group provider missing")
	}
	if _, ok := providers["work-provider"]; ok {
		t.Fatal("disabled group provider should be excluded")
	}
	rules := result["rules"].([]any)
	if len(rules) != 2 || rules[0] != "DOMAIN-SUFFIX,github.com,PROXY" || rules[1] != "MATCH,DIRECT" {
		t.Fatalf("unexpected rules: %#v", rules)
	}
}
