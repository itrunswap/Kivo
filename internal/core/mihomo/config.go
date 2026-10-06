package mihomo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	appconfig "github.com/itrunswap/Kivo/internal/config"
)

// GenerateConfig 生成 Mihomo 可以直接读取的 JSON 配置。
// JSON 是 YAML 的严格子集，避免手工拼接 YAML 时因订阅名或 URL 特殊字符造成语法问题。
func GenerateConfig(cfg appconfig.Config, runtimeDir string, options ...RuntimeOptions) (string, error) {
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("创建 Mihomo 运行目录: %w", err)
	}
	providerDir := filepath.Join(runtimeDir, "proxy_providers")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		return "", fmt.Errorf("创建订阅缓存目录: %w", err)
	}

	providers := map[string]any{}
	providerNames := make([]string, 0, len(cfg.Subscriptions))
	enabledGroups := make(map[string]bool, len(cfg.SubscriptionGroups))
	for _, group := range cfg.SubscriptionGroups {
		enabledGroups[strings.ToLower(group.Name)] = group.Enabled
	}
	for index, sub := range cfg.Subscriptions {
		// 旧测试数据可能没有分组；空分组按默认启用处理。
		groupEnabled := sub.Group == "" || enabledGroups[strings.ToLower(sub.Group)]
		if !sub.Enabled || !groupEnabled {
			continue
		}
		provider := map[string]any{
			"type":     "http",
			"url":      sub.URL,
			"path":     fmt.Sprintf("./proxy_providers/provider-%d.yaml", index+1),
			"interval": defaultInt(sub.UpdateInterval, 3600),
			"proxy":    providerUpdateProxy(sub.UpdateVia),
			"health-check": map[string]any{
				"enable":   true,
				"url":      defaultString(sub.HealthCheckURL, "https://www.gstatic.com/generate_204"),
				"interval": defaultInt(sub.HealthInterval, 300),
				"timeout":  5000,
				"lazy":     true,
			},
		}
		// 不少 V2Board 类订阅站会根据 User-Agent 决定返回 Clash YAML 还是
		// Base64 节点链接。显式使用 Clash.Meta 标识，避免返回 Mihomo provider
		// 无法解析的通用订阅内容。
		header := map[string]any{"User-Agent": []string{"Clash.Meta"}}
		switch strings.ToLower(sub.Auth.Type) {
		case "basic":
			encoded := base64.StdEncoding.EncodeToString([]byte(sub.Auth.Username + ":" + sub.Auth.Secret))
			header["Authorization"] = []string{"Basic " + encoded}
		case "bearer":
			header["Authorization"] = []string{"Bearer " + sub.Auth.Secret}
		case "token":
			header["Authorization"] = []string{"token " + sub.Auth.Secret}
		case "age":
			provider["age-secret-key"] = sub.Auth.Secret
		case "aes":
			if strings.TrimSpace(cfg.Mihomo.ControllerKey) == "" {
				return "", fmt.Errorf("AES 订阅 %s 需要非空的 Mihomo Controller 密钥", sub.Name)
			}
			internalURL, err := internalSubscriptionURL(cfg.Web.Listen, sub.Name)
			if err != nil {
				return "", fmt.Errorf("生成 AES 订阅 %s 的内部地址: %w", sub.Name, err)
			}
			provider["url"] = internalURL
			// AES 的远端下载发生在 Kivo 内部适配器中。本地回环端点必须直连，
			// 由适配器再根据 UpdateVia 决定远端请求是否进入固定 PROXY 出站的内部入口。
			provider["proxy"] = "DIRECT"
			header["X-Kivo-Internal"] = []string{cfg.Mihomo.ControllerKey}
		}
		provider["header"] = header
		if sub.AdditionalPrefix != "" {
			provider["override"] = map[string]any{"additional-prefix": sub.AdditionalPrefix}
		}
		providers[sub.Name] = provider
		providerNames = append(providerNames, sub.Name)
	}

	proxyGroup := map[string]any{
		"name":    "PROXY",
		"type":    "select",
		"proxies": []string{"DIRECT"},
	}
	groups := []any{proxyGroup}
	if len(providerNames) > 0 {
		proxyGroup["proxies"] = []string{"AUTO", "DIRECT"}
		proxyGroup["use"] = providerNames
		groups = append(groups, map[string]any{
			"name": "AUTO", "type": "url-test", "use": providerNames,
			"url": "https://www.gstatic.com/generate_204", "interval": 300,
			"timeout": 5000, "tolerance": 50, "lazy": true,
		})
	}

	rules := generateRules(cfg)
	content := map[string]any{
		"mixed-port":          cfg.Mihomo.MixedPort,
		"allow-lan":           cfg.Mihomo.AllowLAN,
		"mode":                cfg.Mihomo.Mode,
		"log-level":           "info",
		"ipv6":                true,
		"tcp-concurrent":      true,
		"unified-delay":       true,
		"external-controller": cfg.Mihomo.Controller,
		"secret":              cfg.Mihomo.ControllerKey,
		"profile": map[string]any{
			"store-selected": true,
			"store-fake-ip":  true,
		},
		"proxy-providers": providers,
		"proxy-groups":    groups,
		"rules":           rules,
	}
	if len(options) > 0 && options[0].SubscriptionPort > 0 {
		option := options[0]
		if option.SubscriptionSecret == "" {
			return "", fmt.Errorf("内部订阅代理入口缺少认证密钥")
		}
		content["listeners"] = []any{map[string]any{
			"name": "kivo-subscription", "type": "http", "listen": "127.0.0.1",
			"port": option.SubscriptionPort, "proxy": "PROXY",
			"users": []any{map[string]any{"username": "kivo", "password": option.SubscriptionSecret}},
		}}
	}
	if cfg.Mihomo.TUNEnabled {
		content["tun"] = map[string]any{
			"enable": true, "stack": "mixed", "auto-route": true,
			"auto-redirect": true, "auto-detect-interface": true,
			"dns-hijack": []string{"any:53", "tcp://any:53"},
		}
		content["dns"] = map[string]any{
			"enable": true, "ipv6": true, "enhanced-mode": "fake-ip",
			"fake-ip-range":      "198.18.0.1/16",
			"fake-ip-filter":     []string{"*.lan", "*.local", "localhost"},
			"default-nameserver": []string{"1.1.1.1", "8.8.8.8"},
			"nameserver":         []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		}
	}

	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成 Mihomo 配置: %w", err)
	}
	path := filepath.Join(runtimeDir, "config.yaml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("写入 Mihomo 临时配置: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("替换 Mihomo 配置: %w", err)
	}
	return path, nil
}

func providerUpdateProxy(via string) string {
	if strings.EqualFold(strings.TrimSpace(via), "proxy") {
		return "PROXY"
	}
	return "DIRECT"
}

// internalSubscriptionURL 把控制服务的监听地址转换为 Mihomo 可访问的地址。
// 通配监听地址不能作为连接目标，因此分别映射到 IPv4/IPv6 回环地址。
func internalSubscriptionURL(listen, name string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host)
	if host == "" || host == "0.0.0.0" || (ip != nil && ip.IsUnspecified() && ip.To4() != nil) {
		host = "127.0.0.1"
	} else if host == "::" || (ip != nil && ip.IsUnspecified()) {
		host = "::1"
	}
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/api/v1/internal/subscription-content"}
	query := endpoint.Query()
	query.Set("name", name)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func generateRules(cfg appconfig.Config) []string {
	profileName := strings.ToLower(strings.TrimSpace(cfg.Routing.ActiveProfile))
	var profile *appconfig.RouteProfile
	for index := range cfg.Routing.Profiles {
		if strings.EqualFold(cfg.Routing.Profiles[index].Name, profileName) {
			profile = &cfg.Routing.Profiles[index]
			break
		}
	}
	if profile == nil {
		return []string{"MATCH,PROXY"}
	}
	groupByName := make(map[string]appconfig.RuleGroup, len(cfg.Routing.RuleGroups))
	for _, group := range cfg.Routing.RuleGroups {
		groupByName[strings.ToLower(group.Name)] = group
	}
	rules := make([]string, 0, 16)
	for _, groupName := range profile.Groups {
		group, exists := groupByName[strings.ToLower(groupName)]
		if !exists {
			continue
		}
		for _, rule := range group.Rules {
			target := routeTarget(rule.Action)
			kind := strings.ToUpper(strings.TrimSpace(rule.Type))
			kind = strings.ReplaceAll(kind, "_", "-")
			rules = append(rules, kind+","+strings.TrimSpace(rule.Value)+","+target)
		}
	}
	rules = append(rules, "MATCH,"+routeTarget(profile.DefaultAction))
	return rules
}

func routeTarget(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "direct":
		return "DIRECT"
	case "reject":
		return "REJECT"
	default:
		return "PROXY"
	}
}

func defaultInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
