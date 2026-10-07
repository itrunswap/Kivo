package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

// CoreInstallations 返回磁盘中由当前适配器管理的全部内核版本。
func (s *Service) CoreInstallations() ([]core.Installation, error) {
	inventory, ok := s.core.(core.Inventory)
	if !ok {
		return nil, errors.New("当前内核适配器不支持版本管理")
	}
	return inventory.ListInstallations()
}

func (s *Service) ImportCore(ctx context.Context, source string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	inventory, ok := s.core.(core.Inventory)
	if !ok {
		return errors.New("当前内核适配器不支持本地导入")
	}
	return s.protectProxyAfterCoreChange(ctx, func() error { return inventory.Import(ctx, source) })
}

func (s *Service) UseCore(ctx context.Context, reference string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	inventory, ok := s.core.(core.Inventory)
	if !ok {
		return errors.New("当前内核适配器不支持版本切换")
	}
	version, err := resolveCoreReference(inventory, reference)
	if err != nil {
		return err
	}
	return s.protectProxyAfterCoreChange(ctx, func() error { return inventory.Use(ctx, version) })
}

func (s *Service) RemoveCore(ctx context.Context, reference string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	inventory, ok := s.core.(core.Inventory)
	if !ok {
		return errors.New("当前内核适配器不支持版本删除")
	}
	version, err := resolveCoreReference(inventory, reference)
	if err != nil {
		return err
	}
	return inventory.Remove(ctx, version)
}

func (s *Service) PurgeCores(ctx context.Context) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	inventory, ok := s.core.(core.Inventory)
	if !ok {
		return errors.New("当前内核适配器不支持卸载")
	}
	if _, err := s.restoreSystemProxyLocked(ctx, false); err != nil {
		return err
	}
	if err := inventory.Purge(ctx); err != nil {
		return err
	}
	return s.store.Update(func(cfg *config.Config) error {
		cfg.SystemProxy.AutoConnect, cfg.Mihomo.AutoStart = false, false
		return nil
	})
}

func resolveCoreReference(inventory core.Inventory, reference string) (string, error) {
	items, err := inventory.ListInstallations()
	if err != nil {
		return "", err
	}
	if index, err := strconv.Atoi(strings.TrimSpace(reference)); err == nil {
		if index < 1 || index > len(items) {
			return "", fmt.Errorf("内核序号必须在 1-%d 之间", len(items))
		}
		return items[index-1].Version, nil
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimPrefix(item.Version, "v"), strings.TrimPrefix(strings.TrimSpace(reference), "v")) {
			return item.Version, nil
		}
	}
	return "", fmt.Errorf("没有找到内核 %s", reference)
}

// SubscriptionPatch 只修改非 nil 字段；Secret 为 nil 时保留旧凭据。
type SubscriptionPatch struct {
	Revision         string                       `json:"revision,omitempty"`
	DownloadAuth     *SubscriptionCredentialPatch `json:"downloadAuth,omitempty"`
	Decryption       *SubscriptionCredentialPatch `json:"decryption,omitempty"`
	Options          *config.SubscriptionOptions  `json:"options,omitempty"`
	UpdateInterval   *int                         `json:"updateInterval,omitempty"`
	HealthInterval   *int                         `json:"healthInterval,omitempty"`
	Reference        string                       `json:"reference"`
	Name             *string                      `json:"name,omitempty"`
	URL              *string                      `json:"url,omitempty"`
	AuthType         *string                      `json:"authType,omitempty"`
	Username         *string                      `json:"username,omitempty"`
	Secret           *string                      `json:"secret,omitempty"`
	UpdateVia        *string                      `json:"updateVia,omitempty"`
	Group            *string                      `json:"group,omitempty"`
	AdditionalPrefix *string                      `json:"additionalPrefix,omitempty"`
	Enabled          *bool                        `json:"enabled,omitempty"`
}

func (s *Service) PatchSubscription(ctx context.Context, patch SubscriptionPatch) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	if strings.TrimSpace(patch.Reference) == "" {
		return errors.New("缺少订阅名称或序号")
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		index, err := subscriptionIndex(cfg.Subscriptions, patch.Reference)
		if err != nil {
			return err
		}
		sub := &cfg.Subscriptions[index]
		if patch.Revision != "" && patch.Revision != sub.Revision() {
			return errors.New("订阅已被其他窗口修改，请返回后重新打开编辑页；当前输入尚未保存")
		}
		if err := patchSubscriptionCredentials(sub, patch); err != nil {
			return err
		}
		if patch.Options != nil {
			sub.Options = *patch.Options
		}
		if patch.UpdateInterval != nil {
			if *patch.UpdateInterval < 60 || *patch.UpdateInterval > 604800 {
				return errors.New("更新周期须在 60–604800 秒之间")
			}
			sub.UpdateInterval = *patch.UpdateInterval
		}
		if patch.HealthInterval != nil {
			if *patch.HealthInterval < 30 || *patch.HealthInterval > 604800 {
				return errors.New("健康检查周期须在 30–604800 秒之间")
			}
			sub.HealthInterval = *patch.HealthInterval
		}
		if patch.Name != nil {
			sub.Name = strings.TrimSpace(*patch.Name)
		}
		if patch.URL != nil {
			candidate := strings.TrimSpace(*patch.URL)
			parsed, parseErr := url.ParseRequestURI(candidate)
			if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("订阅地址必须是有效的 HTTP 或 HTTPS URL")
			}
			sub.URL = candidate
		}
		if patch.UpdateVia != nil {
			sub.UpdateVia = strings.ToLower(strings.TrimSpace(*patch.UpdateVia))
		}
		if patch.Group != nil {
			group, err := canonicalSubscriptionGroup(cfg.SubscriptionGroups, *patch.Group)
			if err != nil {
				return err
			}
			sub.Group = group
		}
		if patch.AdditionalPrefix != nil {
			sub.AdditionalPrefix = *patch.AdditionalPrefix
		}
		if patch.Enabled != nil {
			sub.Enabled = *patch.Enabled
		}
		return nil
	}); err != nil {
		return err
	}
	return s.reloadSavedSubscription(ctx)
}

func (s *Service) RemoveSubscriptionReference(ctx context.Context, reference string) error {
	name, err := s.subscriptionName(reference)
	if err != nil {
		return err
	}
	return s.RemoveSubscription(ctx, name)
}

func (s *Service) UpdateSubscriptionReference(ctx context.Context, reference string) error {
	_, err := s.UpdateSubscriptions(ctx, SubscriptionUpdateInput{Reference: reference})
	return err
}

// SubscriptionUpdateInput 描述一次批量更新。Reference 为空或 all 时更新全部活动订阅；
// Group 用于只更新指定分组；Via 为空时保留每个订阅当前的更新路径。
type SubscriptionUpdateInput struct {
	Reference string `json:"reference,omitempty"`
	Group     string `json:"group,omitempty"`
	Via       string `json:"via,omitempty"`
}

// SubscriptionUpdateResult 返回成功更新的订阅以及是否为本次操作临时启动过内核。
type SubscriptionUpdateResult struct {
	Results                []SubscriptionActionItem `json:"results"`
	Updated                []string                 `json:"updated"`
	Via                    string                   `json:"via,omitempty"`
	TemporarilyStartedCore bool                     `json:"temporarilyStartedCore"`
}

// SubscriptionTestInput 描述一次订阅健康检查。选择范围和网络路径与更新订阅保持一致。
type SubscriptionTestInput struct {
	Reference string `json:"reference,omitempty"`
	Group     string `json:"group,omitempty"`
	Via       string `json:"via,omitempty"`
}

// SubscriptionTestResult 返回成功触发健康检查的订阅和内核临时启停状态。
type SubscriptionTestResult struct {
	Results                []SubscriptionActionItem `json:"results"`
	Tested                 []string                 `json:"tested"`
	Via                    string                   `json:"via,omitempty"`
	TemporarilyStartedCore bool                     `json:"temporarilyStartedCore"`
}

// UpdateSubscriptions 更新单个、全部或指定分组的活动订阅。
//
// Mihomo 的 provider 刷新只能通过运行中的控制接口触发。当内核原本处于停止状态时，
// 本方法会临时启动内核，并在刷新结束后恢复停止状态，不改变 AutoStart 偏好。
func (s *Service) UpdateSubscriptions(ctx context.Context, input SubscriptionUpdateInput) (SubscriptionUpdateResult, error) {
	if !s.subscriptionAction.TryLock() {
		return SubscriptionUpdateResult{}, errors.New("已有订阅更新或检查正在执行，请等待完成后重试")
	}
	defer s.subscriptionAction.Unlock()
	input.Reference = strings.TrimSpace(input.Reference)
	input.Group = strings.TrimSpace(input.Group)
	input.Via = strings.ToLower(strings.TrimSpace(input.Via))
	result := SubscriptionUpdateResult{Updated: []string{}, Via: input.Via}
	if input.Via != "" && input.Via != "direct" && input.Via != "proxy" {
		return result, errors.New("更新路径必须是 direct 或 proxy")
	}
	targets, err := s.subscriptionUpdateTargets(input.Reference, input.Group)
	if err != nil {
		return result, err
	}

	result.TemporarilyStartedCore, err = s.prepareSubscriptionAction(ctx, targets, input.Via)
	if err != nil {
		return result, err
	}

	var updateErrors []error
	for _, target := range targets {
		item, err := s.performSubscriptionAction(ctx, target, input.Via, nil)
		result.Results = append(result.Results, item)
		if err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("更新订阅 %s: %w", target.Name, err))
			continue
		}
		result.Updated = append(result.Updated, target.Name)
	}
	if stopErr := s.restoreSubscriptionActionCore(result.TemporarilyStartedCore); stopErr != nil {
		updateErrors = append(updateErrors, stopErr)
	}
	return result, errors.Join(updateErrors...)
}

// prepareSubscriptionAction 统一处理 provider 更新和健康检查所需的路径切换、
// 配置重载与临时启动，避免 CLI 和 Web 出现不一致的生命周期行为。
func (s *Service) prepareSubscriptionAction(ctx context.Context, targets []config.Subscription, via string) (bool, error) {
	state := s.core.Status(ctx).State
	if state == core.StateNotInstalled {
		return false, errors.New("Mihomo 尚未安装，请先执行 /core install")
	}
	if state == core.StateStarting || state == core.StateStopping {
		return false, fmt.Errorf("Mihomo 当前处于 %s 状态，请稍后重试", state)
	}

	routeChanged := false
	previousRoutes := make(map[string]string, len(targets))
	if via != "" {
		targetNames := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			targetNames[strings.ToLower(target.Name)] = struct{}{}
		}
		if err := s.store.Update(func(cfg *config.Config) error {
			for index := range cfg.Subscriptions {
				if _, ok := targetNames[strings.ToLower(cfg.Subscriptions[index].Name)]; !ok {
					continue
				}
				current := defaultString(cfg.Subscriptions[index].UpdateVia, "direct")
				if !strings.EqualFold(current, via) {
					previousRoutes[cfg.Subscriptions[index].Name] = cfg.Subscriptions[index].UpdateVia
					cfg.Subscriptions[index].UpdateVia = via
					// AES 从适配器实时读取路径，无需重载 Mihomo；普通 provider 才需应用配置。
					_, decrypt := cfg.Subscriptions[index].Credentials()
					if decrypt.Type != "aes" {
						routeChanged = true
					}
				}
			}
			return nil
		}); err != nil {
			return false, err
		}
	}

	if state == core.StateRunning && routeChanged {
		var err error
		if reloader, ok := s.core.(core.Reloader); ok {
			err = reloader.Reload(ctx)
		} else {
			err = s.core.Restart(ctx)
		}
		if err != nil {
			rollbackErr := s.store.Update(func(cfg *config.Config) error {
				for i := range cfg.Subscriptions {
					if previous, ok := previousRoutes[cfg.Subscriptions[i].Name]; ok {
						cfg.Subscriptions[i].UpdateVia = previous
					}
				}
				return nil
			})
			return false, errors.Join(fmt.Errorf("应用订阅访问路径失败，已尝试恢复原路径: %w", err), rollbackErr)
		}
	}
	if state != core.StateRunning {
		if err := s.core.Start(ctx); err != nil {
			return false, fmt.Errorf("临时启动 Mihomo 以操作订阅: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func (s *Service) restoreSubscriptionActionCore(temporarilyStarted bool) error {
	if !temporarilyStarted {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := s.core.Stop(stopCtx); err != nil {
		return fmt.Errorf("恢复 Mihomo 停止状态: %w", err)
	}
	return nil
}

func (s *Service) subscriptionUpdateTargets(reference, group string) ([]config.Subscription, error) {
	cfg := s.store.Snapshot()
	enabledGroups := make(map[string]bool, len(cfg.SubscriptionGroups))
	groupExists := group == ""
	for _, item := range cfg.SubscriptionGroups {
		enabledGroups[strings.ToLower(item.Name)] = item.Enabled
		if strings.EqualFold(item.Name, group) {
			group, groupExists = item.Name, true
		}
	}
	if !groupExists {
		return nil, fmt.Errorf("订阅分组 %s 不存在", group)
	}
	if group != "" && reference != "" && !strings.EqualFold(reference, "all") {
		return nil, errors.New("不能同时指定单个订阅和订阅分组")
	}
	isActive := func(item config.Subscription) bool {
		return item.Enabled && enabledGroups[strings.ToLower(item.Group)]
	}
	if reference != "" && !strings.EqualFold(reference, "all") {
		index, err := subscriptionIndex(cfg.Subscriptions, reference)
		if err != nil {
			return nil, err
		}
		if !isActive(cfg.Subscriptions[index]) {
			return nil, fmt.Errorf("订阅 %s 或其分组当前未启用，请先启用后再更新", cfg.Subscriptions[index].Name)
		}
		return []config.Subscription{cfg.Subscriptions[index]}, nil
	}
	targets := make([]config.Subscription, 0, len(cfg.Subscriptions))
	for _, item := range cfg.Subscriptions {
		if isActive(item) && (group == "" || strings.EqualFold(item.Group, group)) {
			targets = append(targets, item)
		}
	}
	if len(targets) == 0 {
		if group != "" {
			return nil, fmt.Errorf("分组 %s 中没有可更新的活动订阅", group)
		}
		return nil, errors.New("没有可更新的活动订阅")
	}
	return targets, nil
}

func (s *Service) TestSubscriptionReference(ctx context.Context, reference string) error {
	_, err := s.TestSubscriptions(ctx, SubscriptionTestInput{Reference: reference})
	return err
}

// TestSubscriptions 按订阅、分组或全部范围执行 provider 健康检查。
// 它与更新订阅使用同样的路径和临时启停规则。
func (s *Service) TestSubscriptions(ctx context.Context, input SubscriptionTestInput) (SubscriptionTestResult, error) {
	if !s.subscriptionAction.TryLock() {
		return SubscriptionTestResult{}, errors.New("已有订阅更新或检查正在执行，请等待完成后重试")
	}
	defer s.subscriptionAction.Unlock()
	input.Reference = strings.TrimSpace(input.Reference)
	input.Group = strings.TrimSpace(input.Group)
	input.Via = strings.ToLower(strings.TrimSpace(input.Via))
	result := SubscriptionTestResult{Tested: []string{}, Via: input.Via}
	if input.Via != "" && input.Via != "direct" && input.Via != "proxy" {
		return result, errors.New("检查路径必须是 direct 或 proxy")
	}
	targets, err := s.subscriptionUpdateTargets(input.Reference, input.Group)
	if err != nil {
		return result, err
	}
	tester, ok := s.core.(core.ProviderHealth)
	if !ok {
		return result, errors.New("当前内核不支持订阅健康检查")
	}
	result.TemporarilyStartedCore, err = s.prepareSubscriptionAction(ctx, targets, input.Via)
	if err != nil {
		return result, err
	}
	var testErrors []error
	for _, target := range targets {
		// 先按选定路径刷新 provider，这一步同时验证订阅源可访问、
		// 返回格式可解析且至少含有一个节点；随后再检查节点健康状态。
		item, err := s.performSubscriptionAction(ctx, target, input.Via, tester)
		result.Results = append(result.Results, item)
		if err != nil {
			testErrors = append(testErrors, fmt.Errorf("检查订阅 %s: %w", target.Name, err))
			continue
		}
		result.Tested = append(result.Tested, target.Name)
	}
	if stopErr := s.restoreSubscriptionActionCore(result.TemporarilyStartedCore); stopErr != nil {
		testErrors = append(testErrors, stopErr)
	}
	return result, errors.Join(testErrors...)
}

func (s *Service) subscriptionName(reference string) (string, error) {
	cfg := s.store.Snapshot()
	index, err := subscriptionIndex(cfg.Subscriptions, reference)
	if err != nil {
		return "", err
	}
	return cfg.Subscriptions[index].Name, nil
}

func subscriptionIndex(items []config.Subscription, reference string) (int, error) {
	reference = strings.TrimSpace(reference)
	if index, err := strconv.Atoi(reference); err == nil {
		if index < 1 || index > len(items) {
			return -1, fmt.Errorf("订阅序号必须在 1-%d 之间", len(items))
		}
		return index - 1, nil
	}
	for index, item := range items {
		if strings.EqualFold(item.Name, reference) {
			return index, nil
		}
	}
	return -1, fmt.Errorf("订阅 %s 不存在", reference)
}

// SubscriptionGroups 返回订阅分组的副本。
func (s *Service) SubscriptionGroups() []config.SubscriptionGroup {
	return s.store.Snapshot().SubscriptionGroups
}

func (s *Service) CreateSubscriptionGroup(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("分组名称不能为空")
	}
	return s.store.Update(func(cfg *config.Config) error {
		for _, group := range cfg.SubscriptionGroups {
			if strings.EqualFold(group.Name, name) {
				return fmt.Errorf("订阅分组 %s 已存在", name)
			}
		}
		cfg.SubscriptionGroups = append(cfg.SubscriptionGroups, config.SubscriptionGroup{Name: name, Enabled: true})
		return nil
	})
}

// RenameSubscriptionGroup 原子更新分组名称及其订阅引用；默认分组是稳定的回退目标。
func (s *Service) RenameSubscriptionGroup(ctx context.Context, oldName, newName string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return errors.New("分组名称不能为空")
	}
	if strings.EqualFold(oldName, "default") {
		return errors.New("默认分组不能重命名")
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		index := -1
		for i, group := range cfg.SubscriptionGroups {
			if strings.EqualFold(group.Name, newName) && !strings.EqualFold(group.Name, oldName) {
				return fmt.Errorf("订阅分组 %s 已存在", newName)
			}
			if strings.EqualFold(group.Name, oldName) {
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("订阅分组 %s 不存在", oldName)
		}
		cfg.SubscriptionGroups[index].Name = newName
		for i := range cfg.Subscriptions {
			if strings.EqualFold(cfg.Subscriptions[i].Group, oldName) {
				cfg.Subscriptions[i].Group = newName
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return s.reloadIfRunning(ctx)
}

func (s *Service) SetSubscriptionGroup(ctx context.Context, name string, enabled, exclusive bool) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	if err := s.store.Update(func(cfg *config.Config) error {
		found := false
		for index := range cfg.SubscriptionGroups {
			match := strings.EqualFold(cfg.SubscriptionGroups[index].Name, name)
			if match {
				found = true
			}
			if exclusive {
				cfg.SubscriptionGroups[index].Enabled = match
			} else if match {
				cfg.SubscriptionGroups[index].Enabled = enabled
			}
		}
		if !found {
			return fmt.Errorf("订阅分组 %s 不存在", name)
		}
		return nil
	}); err != nil {
		return err
	}
	return s.reloadIfRunning(ctx)
}

func (s *Service) RemoveSubscriptionGroup(ctx context.Context, name string) error {
	if strings.EqualFold(name, "default") {
		return errors.New("默认分组不能删除")
	}
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	if err := s.store.Update(func(cfg *config.Config) error {
		for _, sub := range cfg.Subscriptions {
			if strings.EqualFold(sub.Group, name) {
				return fmt.Errorf("分组 %s 中仍有订阅，请先移动或删除订阅", name)
			}
		}
		before := len(cfg.SubscriptionGroups)
		cfg.SubscriptionGroups = slices.DeleteFunc(cfg.SubscriptionGroups, func(group config.SubscriptionGroup) bool { return strings.EqualFold(group.Name, name) })
		if len(cfg.SubscriptionGroups) == before {
			return fmt.Errorf("订阅分组 %s 不存在", name)
		}
		return nil
	}); err != nil {
		return err
	}
	return s.reloadIfRunning(ctx)
}

func canonicalSubscriptionGroup(groups []config.SubscriptionGroup, value string) (string, error) {
	for _, group := range groups {
		if strings.EqualFold(group.Name, strings.TrimSpace(value)) {
			return group.Name, nil
		}
	}
	return "", fmt.Errorf("订阅分组 %s 不存在", value)
}

func (s *Service) Routing() config.RoutingConfig { return s.store.Snapshot().Routing }

// RestoreRouteDefaults 只补齐缺失内置项，不覆盖用户现有规则及自定义方案。
func (s *Service) RestoreRouteDefaults(ctx context.Context) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	defaults := config.DefaultRouting()
	current := s.store.Snapshot().Routing
	missing := false
	for _, group := range defaults.RuleGroups {
		if !slices.ContainsFunc(current.RuleGroups, func(item config.RuleGroup) bool { return strings.EqualFold(item.Name, group.Name) }) {
			missing = true
			break
		}
	}
	if !missing {
		for _, profile := range defaults.Profiles {
			if !slices.ContainsFunc(current.Profiles, func(item config.RouteProfile) bool { return strings.EqualFold(item.Name, profile.Name) }) {
				missing = true
				break
			}
		}
	}
	if !missing {
		return nil
	}
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		for _, group := range defaults.RuleGroups {
			if !slices.ContainsFunc(cfg.Routing.RuleGroups, func(item config.RuleGroup) bool { return strings.EqualFold(item.Name, group.Name) }) {
				cfg.Routing.RuleGroups = append(cfg.Routing.RuleGroups, group)
			}
		}
		for _, profile := range defaults.Profiles {
			if !slices.ContainsFunc(cfg.Routing.Profiles, func(item config.RouteProfile) bool { return strings.EqualFold(item.Name, profile.Name) }) {
				cfg.Routing.Profiles = append(cfg.Routing.Profiles, profile)
			}
		}
		return nil
	})
}

func (s *Service) UseRouteProfile(ctx context.Context, name string) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		found := false
		for _, profile := range cfg.Routing.Profiles {
			if strings.EqualFold(profile.Name, name) {
				cfg.Routing.ActiveProfile = profile.Name
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("路由配置 %s 不存在", name)
		}
		switch strings.ToLower(cfg.Routing.ActiveProfile) {
		case "global":
			cfg.Mihomo.Mode = "global"
		case "direct":
			cfg.Mihomo.Mode = "direct"
		default:
			cfg.Mihomo.Mode = "rule"
		}
		return nil
	})
}

func (s *Service) CreateRouteProfile(name, defaultAction string, groups []string) error {
	name, defaultAction = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(defaultAction))
	return s.store.Update(func(cfg *config.Config) error {
		for _, profile := range cfg.Routing.Profiles {
			if strings.EqualFold(profile.Name, name) {
				return fmt.Errorf("路由配置 %s 已存在", name)
			}
		}
		canonical := make([]string, 0, len(groups))
		for _, requested := range groups {
			found := false
			for _, group := range cfg.Routing.RuleGroups {
				if strings.EqualFold(group.Name, requested) {
					canonical = append(canonical, group.Name)
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("规则组 %s 不存在", requested)
			}
		}
		cfg.Routing.Profiles = append(cfg.Routing.Profiles, config.RouteProfile{Name: name, DefaultAction: defaultAction, Groups: canonical})
		return nil
	})
}

func (s *Service) SetRouteProfileGroup(ctx context.Context, profileName, groupName string, attached bool) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		groupExists := false
		for _, group := range cfg.Routing.RuleGroups {
			if strings.EqualFold(group.Name, groupName) {
				groupName, groupExists = group.Name, true
				break
			}
		}
		if !groupExists {
			return fmt.Errorf("规则组 %s 不存在", groupName)
		}
		for index := range cfg.Routing.Profiles {
			profile := &cfg.Routing.Profiles[index]
			if !strings.EqualFold(profile.Name, profileName) {
				continue
			}
			if attached && !slices.ContainsFunc(profile.Groups, func(value string) bool { return strings.EqualFold(value, groupName) }) {
				profile.Groups = append(profile.Groups, groupName)
			}
			if !attached {
				profile.Groups = slices.DeleteFunc(profile.Groups, func(value string) bool { return strings.EqualFold(value, groupName) })
			}
			return nil
		}
		return fmt.Errorf("路由配置 %s 不存在", profileName)
	})
}

func (s *Service) DeleteRouteProfile(name string) error {
	if isBuiltinRouteProfile(name) {
		return errors.New("内置路由方案不能删除；可创建自定义方案")
	}
	return s.store.Update(func(cfg *config.Config) error {
		if strings.EqualFold(cfg.Routing.ActiveProfile, name) {
			return errors.New("不能删除当前活动路由配置，请先切换")
		}
		before := len(cfg.Routing.Profiles)
		cfg.Routing.Profiles = slices.DeleteFunc(cfg.Routing.Profiles, func(profile config.RouteProfile) bool { return strings.EqualFold(profile.Name, name) })
		if before == len(cfg.Routing.Profiles) {
			return fmt.Errorf("路由配置 %s 不存在", name)
		}
		return nil
	})
}

func isBuiltinRouteProfile(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "global", "direct", "rule", "bypass-cn", "proxy-only", "bypass-list":
		return true
	}
	return false
}

func (s *Service) CreateRuleGroup(name string) error {
	name = strings.TrimSpace(name)
	return s.store.Update(func(cfg *config.Config) error {
		for _, group := range cfg.Routing.RuleGroups {
			if strings.EqualFold(group.Name, name) {
				return fmt.Errorf("规则组 %s 已存在", name)
			}
		}
		cfg.Routing.RuleGroups = append(cfg.Routing.RuleGroups, config.RuleGroup{Name: name, Rules: []config.RouteRule{}})
		return nil
	})
}

func (s *Service) DeleteRuleGroup(name string) error {
	switch strings.TrimSpace(name) {
	case "中国大陆直连", "指定地址代理", "指定地址直连":
		return errors.New("内置规则组不能删除；可清空或创建自定义规则组")
	}
	return s.store.Update(func(cfg *config.Config) error {
		for _, profile := range cfg.Routing.Profiles {
			if slices.ContainsFunc(profile.Groups, func(value string) bool { return strings.EqualFold(value, name) }) {
				return fmt.Errorf("规则组 %s 仍被路由配置 %s 使用", name, profile.Name)
			}
		}
		before := len(cfg.Routing.RuleGroups)
		cfg.Routing.RuleGroups = slices.DeleteFunc(cfg.Routing.RuleGroups, func(group config.RuleGroup) bool { return strings.EqualFold(group.Name, name) })
		if before == len(cfg.Routing.RuleGroups) {
			return fmt.Errorf("规则组 %s 不存在", name)
		}
		return nil
	})
}

func (s *Service) AddRouteRule(ctx context.Context, groupName string, rule config.RouteRule) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		for index := range cfg.Routing.RuleGroups {
			if strings.EqualFold(cfg.Routing.RuleGroups[index].Name, groupName) {
				cfg.Routing.RuleGroups[index].Rules = append(cfg.Routing.RuleGroups[index].Rules, rule)
				return nil
			}
		}
		return fmt.Errorf("规则组 %s 不存在", groupName)
	})
}

func (s *Service) RemoveRouteRule(ctx context.Context, groupName string, index int) error {
	return s.RemoveRouteRuleChecked(ctx, groupName, index, nil)
}

// RemoveRouteRuleChecked 在有预期规则时拒绝过期的序号，避免多端并发误删。
func (s *Service) RemoveRouteRuleChecked(ctx context.Context, groupName string, index int, expected *config.RouteRule) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		for groupIndex := range cfg.Routing.RuleGroups {
			group := &cfg.Routing.RuleGroups[groupIndex]
			if !strings.EqualFold(group.Name, groupName) {
				continue
			}
			if len(group.Rules) == 0 {
				return fmt.Errorf("规则组 %s 尚无规则，无需删除", groupName)
			}
			if index < 1 || index > len(group.Rules) {
				return fmt.Errorf("规则序号必须在 1-%d 之间", len(group.Rules))
			}
			if expected != nil && group.Rules[index-1] != *expected {
				return errors.New("规则列表已变化，请刷新后再删除")
			}
			group.Rules = slices.Delete(group.Rules, index-1, index)
			return nil
		}
		return fmt.Errorf("规则组 %s 不存在", groupName)
	})
}

// UpdateRouteRule 替换指定规则；预期旧值使桌面与 Web 的并发编辑可检测。
func (s *Service) UpdateRouteRule(ctx context.Context, groupName string, index int, expected, replacement config.RouteRule) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		for i := range cfg.Routing.RuleGroups {
			group := &cfg.Routing.RuleGroups[i]
			if !strings.EqualFold(group.Name, groupName) {
				continue
			}
			if index < 1 || index > len(group.Rules) {
				return errors.New("规则序号已变化，请刷新后重试")
			}
			if group.Rules[index-1] != expected {
				return errors.New("规则已被其他窗口修改，请刷新后重试")
			}
			group.Rules[index-1] = replacement
			return nil
		}
		return fmt.Errorf("规则组 %s 不存在", groupName)
	})
}

// MoveRouteRule 只移动组内规则，维持 Mihomo 的从上到下匹配语义。
func (s *Service) MoveRouteRule(ctx context.Context, groupName string, from, to int, expected config.RouteRule) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.applyRoutingUpdate(ctx, func(cfg *config.Config) error {
		for i := range cfg.Routing.RuleGroups {
			group := &cfg.Routing.RuleGroups[i]
			if !strings.EqualFold(group.Name, groupName) {
				continue
			}
			if from < 1 || from > len(group.Rules) || to < 1 || to > len(group.Rules) {
				return errors.New("规则序号已变化，请刷新后重试")
			}
			if group.Rules[from-1] != expected {
				return errors.New("规则已被其他窗口修改，请刷新后重试")
			}
			rule := group.Rules[from-1]
			group.Rules = slices.Delete(group.Rules, from-1, from)
			group.Rules = slices.Insert(group.Rules, to-1, rule)
			return nil
		}
		return fmt.Errorf("规则组 %s 不存在", groupName)
	})
}

// applyRoutingUpdate 在内核拒绝新规则时恢复旧持久配置，避免报错后留下无法启动的规则。
// 调用方已持有 subscriptionAction 锁，确保路由操作不会相互穿插。
func (s *Service) applyRoutingUpdate(ctx context.Context, change func(*config.Config) error) error {
	before := s.store.Snapshot()
	candidate, err := s.store.Preview(change)
	if err != nil {
		return err
	}
	if validator, ok := s.core.(interface {
		ValidateConfig(context.Context, config.Config) error
	}); ok {
		if err := validator.ValidateConfig(ctx, candidate); err != nil {
			return err
		}
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		if !reflect.DeepEqual(*cfg, before) {
			return errors.New("配置已被其他窗口修改，请刷新后重试")
		}
		return change(cfg)
	}); err != nil {
		return err
	}
	if err := s.reloadIfRunning(ctx); err != nil {
		applied := s.store.Snapshot()
		rollbackErr := s.store.Update(func(cfg *config.Config) error {
			if !reflect.DeepEqual(cfg.Routing, applied.Routing) || cfg.Mihomo.Mode != applied.Mihomo.Mode {
				return errors.New("其他客户端已修改路由，不能安全回滚")
			}
			cfg.Routing = before.Routing
			cfg.Mihomo.Mode = before.Mihomo.Mode
			return nil
		})
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("新路由未能应用；配置可能已保存: %w", err), rollbackErr)
		}
		if s.core.Status(ctx).State != core.StateRunning {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			recoveryErr := s.core.Restart(recoveryCtx)
			cancel()
			if recoveryErr != nil {
				return errors.Join(fmt.Errorf("新路由未能应用，已恢复上一次配置，但内核未能恢复运行: %w", err), recoveryErr)
			}
		}
		return fmt.Errorf("新路由未能应用，已恢复上一次配置: %w", err)
	}
	return nil
}
