// Package config 负责应用配置的默认值、校验和原子持久化。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const currentSchemaVersion = 2

// Config 是 Kivo 的持久化配置。
// 配置中可能包含订阅凭据，因此配置文件始终以仅当前用户可读写的权限保存。
type Config struct {
	SchemaVersion      int                 `json:"schemaVersion"`
	Web                WebConfig           `json:"web"`
	Mihomo             MihomoConfig        `json:"mihomo"`
	SubscriptionGroups []SubscriptionGroup `json:"subscriptionGroups"`
	Subscriptions      []Subscription      `json:"subscriptions"`
	Routing            RoutingConfig       `json:"routing"`
	SelectedNode       string              `json:"selectedNode,omitempty"`
	SystemProxy        SystemProxyConfig   `json:"systemProxy"`
}

// SystemProxyConfig 只备份本程序接管的设置，不导出到公开设置接口。
type SystemProxyConfig struct {
	AutoConnect bool              `json:"autoConnect"`
	Lease       *SystemProxyLease `json:"lease,omitempty"`
}

// SystemProxyLease 是写入前保存的事务日志；异常退出后仍能恢复，不依赖内存。
type SystemProxyLease struct {
	Before   json.RawMessage `json:"before"`
	Applied  json.RawMessage `json:"applied"`
	Endpoint string          `json:"endpoint"`
	Phase    string          `json:"phase"` // prepared、active、conflict。
}

// WebConfig 控制管理页面和本地控制 API。
type WebConfig struct {
	Listen string `json:"listen"`
	Secret string `json:"secret"`
}

// MihomoConfig 描述由 Kivo 管理的 Mihomo 运行参数。
type MihomoConfig struct {
	BinaryPath    string `json:"binaryPath,omitempty"`
	Version       string `json:"version,omitempty"`
	AutoStart     bool   `json:"autoStart"`
	Controller    string `json:"controller"`
	ControllerKey string `json:"controllerKey"`
	MixedPort     int    `json:"mixedPort"`
	Mode          string `json:"mode"`
	AllowLAN      bool   `json:"allowLAN"`
	TUNEnabled    bool   `json:"tunEnabled"`
	DownloadProxy string `json:"downloadProxy,omitempty"`
	DownloadRetry int    `json:"downloadRetry"`
}

// SubscriptionAuth 描述订阅端点的认证方式。
type SubscriptionAuth struct {
	Type     string `json:"type,omitempty"` // none、basic、bearer、token、age 或 aes。
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

// Subscription 是一个由 Mihomo proxy-provider 管理的订阅。
type Subscription struct {
	Name             string           `json:"name"`
	URL              string           `json:"url"`
	Auth             SubscriptionAuth `json:"auth,omitempty"`
	UpdateVia        string           `json:"updateVia,omitempty"` // direct 或 proxy；空值兼容旧配置并按 direct 处理。
	UpdateInterval   int              `json:"updateInterval"`
	HealthInterval   int              `json:"healthInterval"`
	HealthCheckURL   string           `json:"healthCheckURL"`
	Enabled          bool             `json:"enabled"`
	Group            string           `json:"group"`
	AdditionalPrefix string           `json:"additionalPrefix,omitempty"`
}

// SubscriptionGroup 将多个供应商组织成可以独占切换或组合启用的集合。
type SubscriptionGroup struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// RouteRule 是一条结构化路由规则。Type 使用 Mihomo 规则类型的小写形式，
// Action 为 proxy、direct 或 reject；序号即优先级，越靠前越先匹配。
type RouteRule struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Action string `json:"action"`
}

// RuleGroup 是可被多个路由配置复用的有序规则集合。
type RuleGroup struct {
	Name  string      `json:"name"`
	Rules []RouteRule `json:"rules"`
}

// RouteProfile 组合规则组并定义未命中规则时的默认动作。
type RouteProfile struct {
	Name          string   `json:"name"`
	DefaultAction string   `json:"defaultAction"`
	Groups        []string `json:"groups"`
}

// RoutingConfig 保存用户可切换的路由配置。
type RoutingConfig struct {
	ActiveProfile string         `json:"activeProfile"`
	Profiles      []RouteProfile `json:"profiles"`
	RuleGroups    []RuleGroup    `json:"ruleGroups"`
}

// Paths 汇总程序使用的目录，便于测试和未来迁移。
type Paths struct {
	Root        string
	ConfigFile  string
	RuntimeDir  string
	CoreDir     string
	DownloadDir string
	LogDir      string
}

// ResolvePaths 根据用户指定目录或系统用户配置目录生成路径。
func ResolvePaths(root string) (Paths, error) {
	if strings.TrimSpace(root) == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Paths{}, fmt.Errorf("获取用户配置目录: %w", err)
		}
		root, err = defaultDataDir(base)
		if err != nil {
			return Paths{}, err
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("解析数据目录: %w", err)
	}
	return Paths{
		Root:        abs,
		ConfigFile:  filepath.Join(abs, "config.json"),
		RuntimeDir:  filepath.Join(abs, "runtime"),
		CoreDir:     filepath.Join(abs, "cores"),
		DownloadDir: filepath.Join(abs, "downloads"),
		LogDir:      filepath.Join(abs, "logs"),
	}, nil
}

// defaultDataDir 不迁移正在使用的数据。优先使用 Kivo 配置；仅有旧配置时继续
// 使用原目录，确保订阅凭据、内核路径和系统代理事务备份在改名后仍然有效。
func defaultDataDir(base string) (string, error) {
	preferred := filepath.Join(base, "Kivo")
	for _, candidate := range []string{preferred, filepath.Join(base, "ProxyPilot")} {
		_, err := os.Stat(filepath.Join(candidate, "config.json"))
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("检查数据目录 %s: %w", candidate, err)
		}
	}
	return preferred, nil
}

// Store 为并发安全的配置仓库。
type Store struct {
	mu    sync.RWMutex
	paths Paths
	cfg   Config
}

// Load 加载配置；首次运行时生成安全默认值。
func Load(paths Paths) (*Store, error) {
	for _, dir := range []string{paths.Root, paths.RuntimeDir, paths.CoreDir, paths.DownloadDir, paths.LogDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("创建目录 %s: %w", dir, err)
		}
	}

	store := &Store{paths: paths}
	data, err := os.ReadFile(paths.ConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		store.cfg, err = defaultConfig()
		if err != nil {
			return nil, err
		}
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}
	if err := json.Unmarshal(data, &store.cfg); err != nil {
		return nil, fmt.Errorf("解析配置: %w", err)
	}
	if store.cfg.SchemaVersion == 1 {
		migrateV1(&store.cfg)
		if err := store.saveLocked(); err != nil {
			return nil, fmt.Errorf("迁移配置: %w", err)
		}
	}
	if err := validate(store.cfg); err != nil {
		return nil, fmt.Errorf("配置无效: %w", err)
	}
	return store, nil
}

func defaultConfig() (Config, error) {
	webSecret, err := randomSecret(32)
	if err != nil {
		return Config{}, err
	}
	controllerSecret, err := randomSecret(32)
	if err != nil {
		return Config{}, err
	}
	return Config{
		SchemaVersion: currentSchemaVersion,
		Web: WebConfig{
			Listen: "127.0.0.1:9099",
			Secret: webSecret,
		},
		Mihomo: MihomoConfig{
			Controller:    "127.0.0.1:19090",
			ControllerKey: controllerSecret,
			MixedPort:     17890,
			Mode:          "rule",
			DownloadRetry: 4,
		},
		SubscriptionGroups: []SubscriptionGroup{{Name: "default", Enabled: true}},
		Subscriptions:      []Subscription{},
		Routing:            defaultRouting(),
	}, nil
}

func defaultRouting() RoutingConfig {
	return RoutingConfig{
		ActiveProfile: "rule",
		Profiles: []RouteProfile{
			{Name: "global", DefaultAction: "proxy"},
			{Name: "direct", DefaultAction: "direct"},
			{Name: "rule", DefaultAction: "proxy"},
			{Name: "bypass-cn", DefaultAction: "proxy", Groups: []string{"中国大陆直连"}},
			{Name: "proxy-only", DefaultAction: "direct", Groups: []string{"指定地址代理"}},
			{Name: "bypass-list", DefaultAction: "proxy", Groups: []string{"指定地址直连"}},
		},
		RuleGroups: []RuleGroup{
			{Name: "中国大陆直连", Rules: []RouteRule{
				{Type: "ip-cidr", Value: "127.0.0.0/8", Action: "direct"},
				{Type: "ip-cidr", Value: "10.0.0.0/8", Action: "direct"},
				{Type: "ip-cidr", Value: "172.16.0.0/12", Action: "direct"},
				{Type: "ip-cidr", Value: "192.168.0.0/16", Action: "direct"},
				{Type: "geosite", Value: "cn", Action: "direct"},
				{Type: "geoip", Value: "cn", Action: "direct"},
			}},
			{Name: "指定地址代理", Rules: []RouteRule{}},
			{Name: "指定地址直连", Rules: []RouteRule{}},
		},
	}
}

// migrateV1 只做无损结构补全；敏感字段和用户现有设置保持不变。
func migrateV1(cfg *Config) {
	cfg.SchemaVersion = currentSchemaVersion
	if cfg.Mihomo.DownloadRetry <= 0 {
		cfg.Mihomo.DownloadRetry = 4
	}
	cfg.SubscriptionGroups = []SubscriptionGroup{{Name: "default", Enabled: true}}
	for index := range cfg.Subscriptions {
		cfg.Subscriptions[index].Group = "default"
	}
	cfg.Routing = defaultRouting()
}

func randomSecret(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成安全密钥: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Snapshot 返回深拷贝，避免调用方绕过 Update 修改内部状态。
func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}

// Update 在锁内修改、校验并原子保存配置。
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cfg
	next = cloneConfig(s.cfg)
	if err := fn(&next); err != nil {
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	old := s.cfg
	s.cfg = next
	if err := s.saveLocked(); err != nil {
		s.cfg = old
		return err
	}
	return nil
}

func cloneConfig(cfg Config) Config {
	copyCfg := cfg
	if cfg.SystemProxy.Lease != nil {
		lease := *cfg.SystemProxy.Lease
		lease.Before = slices.Clone(lease.Before)
		lease.Applied = slices.Clone(lease.Applied)
		copyCfg.SystemProxy.Lease = &lease
	}
	copyCfg.Subscriptions = slices.Clone(cfg.Subscriptions)
	copyCfg.SubscriptionGroups = slices.Clone(cfg.SubscriptionGroups)
	copyCfg.Routing.Profiles = slices.Clone(cfg.Routing.Profiles)
	for index := range copyCfg.Routing.Profiles {
		copyCfg.Routing.Profiles[index].Groups = slices.Clone(cfg.Routing.Profiles[index].Groups)
	}
	copyCfg.Routing.RuleGroups = slices.Clone(cfg.Routing.RuleGroups)
	for index := range copyCfg.Routing.RuleGroups {
		copyCfg.Routing.RuleGroups[index].Rules = slices.Clone(cfg.Routing.RuleGroups[index].Rules)
	}
	return copyCfg
}

// Paths 返回只读路径值。
func (s *Store) Paths() Paths { return s.paths }

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	tmp := s.paths.ConfigFile + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("打开临时配置: %w", err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	// 代理接管日志必须先落盘，再执行 OS 写入。Sync 降低进程/系统异常时只留下半事务的风险。
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("持久化临时配置: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("设置配置权限: %w", err)
	}
	if err := os.Rename(tmp, s.paths.ConfigFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("原子替换配置: %w", err)
	}
	return nil
}

func validate(cfg Config) error {
	if cfg.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf("不支持的配置版本 %d", cfg.SchemaVersion)
	}
	// Web 密钥允许为空或任意长度：空值表示显式关闭鉴权，强度由用户自行决定。
	if _, _, err := net.SplitHostPort(cfg.Web.Listen); err != nil {
		return fmt.Errorf("Web 监听地址格式错误: %w", err)
	}
	if _, _, err := net.SplitHostPort(cfg.Mihomo.Controller); err != nil {
		return fmt.Errorf("Mihomo 控制地址格式错误: %w", err)
	}
	if cfg.Mihomo.MixedPort < 1 || cfg.Mihomo.MixedPort > 65535 {
		return errors.New("mixedPort 必须在 1-65535 之间")
	}
	if cfg.Mihomo.Mode != "rule" && cfg.Mihomo.Mode != "global" && cfg.Mihomo.Mode != "direct" {
		return errors.New("mode 必须是 rule、global 或 direct")
	}
	if cfg.Mihomo.DownloadRetry < 1 || cfg.Mihomo.DownloadRetry > 10 {
		return errors.New("downloadRetry 必须在 1-10 之间")
	}
	if proxyAddress := strings.TrimSpace(cfg.Mihomo.DownloadProxy); proxyAddress != "" && !strings.EqualFold(proxyAddress, "system") {
		parsed, err := url.Parse(proxyAddress)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("downloadProxy 必须是有效的 http:// 或 https:// 地址")
		}
	}
	groupNames := make(map[string]struct{}, len(cfg.SubscriptionGroups))
	for _, group := range cfg.SubscriptionGroups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			return errors.New("订阅分组名称不能为空")
		}
		key := strings.ToLower(name)
		if _, exists := groupNames[key]; exists {
			return fmt.Errorf("订阅分组名称重复: %s", name)
		}
		groupNames[key] = struct{}{}
	}
	if len(groupNames) == 0 {
		return errors.New("至少需要一个订阅分组")
	}
	names := make(map[string]struct{}, len(cfg.Subscriptions))
	for _, sub := range cfg.Subscriptions {
		name := strings.TrimSpace(sub.Name)
		if name == "" {
			return errors.New("订阅名称不能为空")
		}
		nameKey := strings.ToLower(name)
		if _, exists := names[nameKey]; exists {
			return fmt.Errorf("订阅名称重复: %s", name)
		}
		names[nameKey] = struct{}{}
		parsedURL, err := url.Parse(strings.TrimSpace(sub.URL))
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
			return fmt.Errorf("订阅 %s 的 URL 必须是有效的 HTTP/HTTPS 地址", name)
		}
		authType := strings.ToLower(strings.TrimSpace(sub.Auth.Type))
		if authType == "" {
			authType = "none"
		}
		if !slices.Contains([]string{"none", "basic", "bearer", "token", "age", "aes"}, authType) {
			return fmt.Errorf("订阅 %s 的认证类型无效", name)
		}
		if authType != "none" && strings.TrimSpace(sub.Auth.Secret) == "" {
			return fmt.Errorf("订阅 %s 的认证密码或 Token 不能为空", name)
		}
		if authType == "basic" && strings.TrimSpace(sub.Auth.Username) == "" {
			return fmt.Errorf("订阅 %s 的 Basic 认证用户名不能为空", name)
		}
		updateVia := strings.ToLower(strings.TrimSpace(sub.UpdateVia))
		if updateVia != "" && updateVia != "direct" && updateVia != "proxy" {
			return fmt.Errorf("订阅 %s 的更新路径必须是 direct 或 proxy", name)
		}
		if _, exists := groupNames[strings.ToLower(strings.TrimSpace(sub.Group))]; !exists {
			return fmt.Errorf("订阅 %s 引用了不存在的分组 %s", name, sub.Group)
		}
	}
	if err := validateRouting(cfg.Routing); err != nil {
		return err
	}
	return nil
}

func validateRouting(routing RoutingConfig) error {
	actions := []string{"proxy", "direct", "reject"}
	types := []string{"domain", "domain-suffix", "domain-keyword", "domain-wildcard", "domain-regex", "ip-cidr", "ip-cidr6", "geoip", "geosite", "process-name", "process-path", "dst-port"}
	groups := make(map[string]struct{}, len(routing.RuleGroups))
	for _, group := range routing.RuleGroups {
		if strings.TrimSpace(group.Name) == "" {
			return errors.New("规则组名称不能为空")
		}
		key := strings.ToLower(group.Name)
		if _, exists := groups[key]; exists {
			return fmt.Errorf("规则组名称重复: %s", group.Name)
		}
		groups[key] = struct{}{}
		for _, rule := range group.Rules {
			if !slices.Contains(types, strings.ToLower(rule.Type)) || strings.TrimSpace(rule.Value) == "" || !slices.Contains(actions, strings.ToLower(rule.Action)) {
				return fmt.Errorf("规则组 %s 中存在无效规则", group.Name)
			}
		}
	}
	profiles := make(map[string]struct{}, len(routing.Profiles))
	for _, profile := range routing.Profiles {
		key := strings.ToLower(strings.TrimSpace(profile.Name))
		if key == "" || !slices.Contains(actions, strings.ToLower(profile.DefaultAction)) {
			return fmt.Errorf("路由配置 %s 无效", profile.Name)
		}
		if _, exists := profiles[key]; exists {
			return fmt.Errorf("路由配置名称重复: %s", profile.Name)
		}
		profiles[key] = struct{}{}
		for _, group := range profile.Groups {
			if _, exists := groups[strings.ToLower(group)]; !exists {
				return fmt.Errorf("路由配置 %s 引用了不存在的规则组 %s", profile.Name, group)
			}
		}
	}
	if _, exists := profiles[strings.ToLower(routing.ActiveProfile)]; !exists {
		return fmt.Errorf("当前路由配置不存在: %s", routing.ActiveProfile)
	}
	return nil
}
