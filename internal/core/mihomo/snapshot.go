package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

// ProviderSnapshot 从内核实际解析结果生成统计，不以 HTTP 下载成功代替解析成功。
func (c *Client) ProviderSnapshot(ctx context.Context, name string) (core.ProviderSnapshot, error) {
	providers, err := c.providerCatalog(ctx)
	if err != nil {
		return core.ProviderSnapshot{}, err
	}
	for key, provider := range providers {
		if !strings.EqualFold(key, name) && !strings.EqualFold(provider.Name, name) {
			continue
		}
		updatedAt, _ := time.Parse(time.RFC3339Nano, provider.UpdatedAt)
		snapshot := core.ProviderSnapshot{Nodes: []core.Node{}, UpdatedAt: updatedAt}
		for _, info := range provider.Proxies {
			node := core.Node{Name: info.Name, Type: info.Type, ProviderName: name, UDP: info.UDP, Alive: info.Alive}
			if len(info.History) > 0 {
				node.Tested = true
				node.Delay = info.History[len(info.History)-1].Delay
			}
			snapshot.Nodes = append(snapshot.Nodes, node)
		}
		return snapshot, nil
	}
	return core.ProviderSnapshot{}, fmt.Errorf("Mihomo 中不存在订阅 provider %s", name)
}

// snapshotPath 将来源与认证纳入指纹，换 URL、密码或前缀后不会误显示旧节点。
// 更新路径不影响节点身份，切换 direct/proxy 时仍可对比上一次的节点数量。
func (m *Manager) snapshotPath(name string) (string, error) {
	for _, sub := range m.store.Snapshot().Subscriptions {
		if !strings.EqualFold(sub.Name, name) {
			continue
		}
		identity := struct{ Name, URL, Type, Username, Secret, Prefix string }{
			sub.Name, sub.URL, sub.Auth.Type, sub.Auth.Username, sub.Auth.Secret, sub.AdditionalPrefix,
		}
		data, _ := json.Marshal(identity)
		// 旧订阅继续命中原缓存；新增分层凭据或筛选覆盖后不能展示旧快照。
		if sub.Decryption.Type != "" || sub.Options != (config.SubscriptionOptions{}) {
			extra, _ := json.Marshal(struct {
				Decryption config.SubscriptionDecryption
				Options    config.SubscriptionOptions
			}{sub.Decryption, sub.Options})
			data = append(data, extra...)
		}
		sum := sha256.Sum256(data)
		return filepath.Join(m.store.Paths().RuntimeDir, "node-snapshots", hex.EncodeToString(sum[:])+".json"), nil
	}
	return "", fmt.Errorf("订阅 %s 不存在", name)
}

func (m *Manager) saveSnapshot(name string, snapshot core.ProviderSnapshot) error {
	path, err := m.snapshotPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// 不持久化瞬时健康状态，避免离线查看时误认为节点仍在线。
	for index := range snapshot.Nodes {
		snapshot.Nodes[index].Alive = false
		snapshot.Nodes[index].Delay = 0
		snapshot.Nodes[index].Cached = true
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (m *Manager) readSnapshot(name string) (core.ProviderSnapshot, error) {
	path, err := m.snapshotPath(name)
	if err != nil {
		return core.ProviderSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return core.ProviderSnapshot{}, err
	}
	var snapshot core.ProviderSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	for i := range snapshot.Nodes {
		snapshot.Nodes[i].Cached = true
		snapshot.Nodes[i].Alive = false
		snapshot.Nodes[i].Delay = 0
	}
	return snapshot, nil
}

// SubscriptionSnapshot 运行中查询内核，停止后只读取与当前来源匹配的快照。
func (m *Manager) SubscriptionSnapshot(ctx context.Context, name string) (core.ProviderSnapshot, error) {
	m.mu.RLock()
	state := m.state
	m.mu.RUnlock()
	if state != core.StateRunning {
		return m.readSnapshot(name)
	}
	cfg := m.store.Snapshot()
	return NewClient(cfg.Mihomo.Controller, cfg.Mihomo.ControllerKey).ProviderSnapshot(ctx, name)
}

func (m *Manager) cachedNodes() []core.Node {
	cfg := m.store.Snapshot()
	groups := map[string]bool{"": true}
	for _, group := range cfg.SubscriptionGroups {
		groups[strings.ToLower(group.Name)] = group.Enabled
	}
	nodes := []core.Node{}
	seen := map[string]bool{}
	for _, sub := range cfg.Subscriptions {
		if !sub.Enabled || !groups[strings.ToLower(sub.Group)] {
			continue
		}
		snapshot, err := m.readSnapshot(sub.Name)
		if err != nil {
			continue
		}
		for _, node := range snapshot.Nodes {
			if !seen[node.Name] {
				seen[node.Name] = true
				nodes = append(nodes, node)
			}
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}
