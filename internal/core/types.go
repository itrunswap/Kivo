// Package core 定义与具体代理内核无关的领域模型。
package core

import (
	"context"
	"net/url"
	"time"
)

// State 表示代理内核运行状态。
type State string

const (
	StateNotInstalled State = "not_installed"
	StateStopped      State = "stopped"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateStopping     State = "stopping"
	StateFailed       State = "failed"
)

// Status 是对外展示的统一内核状态。
type Status struct {
	Name          string `json:"name"`
	Version       string `json:"version,omitempty"`
	State         State  `json:"state"`
	PID           int    `json:"pid,omitempty"`
	MixedPort     int    `json:"mixedPort,omitempty"`
	Mode          string `json:"mode,omitempty"`
	CurrentNode   string `json:"currentNode,omitempty"`
	EffectiveNode string `json:"effectiveNode,omitempty"` // PROXY 策略组展开后的实际节点，不等同所有流量出口。
	Error         string `json:"error,omitempty"`
}

// Node 是不同内核对节点信息的统一表达。
type Node struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Alive        bool   `json:"alive"`
	Tested       bool   `json:"tested"` // 有健康检查历史才可判定通过；alive 默认值不代表已验证。
	Delay        int    `json:"delay,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	UDP          bool   `json:"udp"`
	Cached       bool   `json:"cached,omitempty"` // 离线快照不代表节点当前在线。
}

// ProviderSnapshot 仅包含可公开的节点元信息，不保存节点密码或订阅原文。
type ProviderSnapshot struct {
	Nodes     []Node    `json:"nodes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ProviderReporter 提供运行时或离线的订阅元信息，供结果统计使用。
type ProviderReporter interface {
	SubscriptionSnapshot(context.Context, string) (ProviderSnapshot, error)
}

// SubscriptionProxy 提供固定 PROXY 出站的内部下载入口，不能复用受规则影响的混合端口。
// 返回的 URL 含内部认证信息，只能用于 HTTP Transport，严禁展示或写入日志。
type SubscriptionProxy interface {
	SubscriptionProxyURL(context.Context) (*url.URL, error)
}

// Reloader 在保持常驻进程不变的情况下应用最新配置。
type Reloader interface{ Reload(context.Context) error }

// InstallEvent 用于 CLI 和 Web 展示内核安装进度。
type InstallEvent struct {
	Stage          string `json:"stage"`
	Message        string `json:"message"`
	Downloaded     int64  `json:"downloaded,omitempty"`
	Total          int64  `json:"total,omitempty"`
	BytesPerSecond int64  `json:"bytesPerSecond,omitempty"`
	Error          string `json:"error,omitempty"` // 流式安装的终止错误；done 才代表业务安装全部完成。
}

// Installation 描述磁盘中一个可切换的内核版本。
type Installation struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Active  bool   `json:"active"`
}

// Inventory 是可选的内核版本仓库能力。运行控制与版本文件管理分离，未来的
// sing-box 适配器可以独立实现，而不影响只需要 Adapter 的业务代码。
type Inventory interface {
	ListInstallations() ([]Installation, error)
	Import(ctx context.Context, source string) error
	Use(ctx context.Context, version string) error
	Remove(ctx context.Context, version string) error
	Purge(ctx context.Context) error
}

// ProviderHealth 是支持单独触发订阅健康检查的可选能力。
type ProviderHealth interface {
	TestSubscription(ctx context.Context, name string) error
}

// Adapter 是业务层依赖的最小内核能力集合。
// 新增 sing-box 或 Xray 时，应实现此接口，而不修改 CLI/Web。
type Adapter interface {
	Install(ctx context.Context, version string, progress func(InstallEvent)) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Status(ctx context.Context) Status
	ListNodes(ctx context.Context) ([]Node, error)
	TestNodes(ctx context.Context) (map[string]int, error)
	SelectNode(ctx context.Context, name string) error
	UpdateSubscription(ctx context.Context, name string) error
	SetMode(ctx context.Context, mode string) error
	Logs(limit int) []string
}
