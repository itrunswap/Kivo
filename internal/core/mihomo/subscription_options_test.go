package mihomo

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
)

func TestSubscriptionOptionsAndCredentialLayersReachProvider(t *testing.T) {
	for _, encryption := range []string{"none", "aes", "age"} {
		t.Run(encryption, func(t *testing.T) {
			cfg := config.Config{Web: config.WebConfig{Listen: "127.0.0.1:9099"}, Mihomo: config.MihomoConfig{ControllerKey: "internal"}, Subscriptions: []config.Subscription{{
				Name: "A", URL: "https://example.com/sub", Enabled: true,
				Auth:       config.SubscriptionAuth{Type: "bearer", Secret: "download-secret"},
				Decryption: config.SubscriptionDecryption{Type: encryption, Secret: "decrypt-secret"},
				Options:    config.SubscriptionOptions{UserAgent: "custom", Filter: "香港|日本", ExcludeFilter: "过期", UDP: "off", TFO: "on", SkipCertVerify: "default"},
			}}}
			path, err := GenerateConfig(cfg, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var output map[string]any
			if err = json.Unmarshal(b, &output); err != nil {
				t.Fatal(err)
			}
			p := output["proxy-providers"].(map[string]any)["A"].(map[string]any)
			o := p["override"].(map[string]any)
			if o["udp"] != false || o["tfo"] != true || o["skip-cert-verify"] != nil || p["filter"] != "香港|日本" || p["exclude-filter"] != "过期" {
				t.Fatal("incorrect filter/override semantics")
			}
			h := p["header"].(map[string]any)
			if encryption == "aes" {
				if h["Authorization"] != nil || strings.Contains(string(b), "download-secret") || strings.Contains(string(b), "decrypt-secret") {
					t.Fatal("remote credentials leaked into internal provider")
				}
			} else if h["Authorization"].([]any)[0] != "Bearer download-secret" || h["User-Agent"].([]any)[0] != "custom" {
				t.Fatal("HTTP credentials or UA missing")
			}
			if encryption == "age" && p["age-secret-key"] != "decrypt-secret" {
				t.Fatal("age key missing")
			}
		})
	}
}
