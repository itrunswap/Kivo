package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

// ConnectOptions 对现有代理的覆盖/接管必须显式选择，默认不覆盖其他客户端或 PAC。
type ConnectOptions struct {
	Replace   bool `json:"replace"`
	Adopt     bool `json:"adopt"`
	SkipCheck bool `json:"skipCheck"`
}

type ProxyOperationResult struct {
	Message      string                     `json:"message"`
	Warning      bool                       `json:"warning"`
	SystemProxy  platform.SystemProxyStatus `json:"systemProxy"`
	Connectivity *ConnectivityReport        `json:"connectivity,omitempty"`
}

func decodeProxyLease(lease *config.SystemProxyLease) (platform.ProxySnapshot, platform.ProxySnapshot, error) {
	var before, applied platform.ProxySnapshot
	if lease == nil || (lease.Phase != "prepared" && lease.Phase != "active" && lease.Phase != "conflict") {
		return before, applied, errors.New("系统代理备份阶段无效，拒绝自动写入")
	}
	if err := json.Unmarshal(lease.Before, &before); err != nil {
		return before, applied, errors.New("系统代理原设置备份损坏，拒绝自动写入")
	}
	if err := json.Unmarshal(lease.Applied, &applied); err != nil {
		return before, applied, errors.New("系统代理接管记录损坏，拒绝自动写入")
	}
	if before.Backend == "" || before.Scope == "" || before.Backend != applied.Backend || before.Scope != applied.Scope || len(before.Entries) == 0 || len(before.Entries) != len(applied.Entries) {
		return before, applied, errors.New("系统代理备份范围无效，拒绝自动写入")
	}
	seen := map[string]bool{}
	for index, entry := range before.Entries {
		other := applied.Entries[index]
		if entry.Name == "" || seen[entry.Name] || entry.Name != other.Name || len(entry.Values) == 0 || len(entry.Values) != len(other.Values) {
			return before, applied, errors.New("系统代理备份字段不完整，拒绝自动写入")
		}
		seen[entry.Name] = true
		for key := range entry.Values {
			if _, exists := other.Values[key]; !exists {
				return before, applied, errors.New("系统代理备份字段不完整，拒绝自动写入")
			}
		}
	}
	if _, _, err := net.SplitHostPort(lease.Endpoint); err != nil {
		return before, applied, errors.New("系统代理备份地址无效")
	}
	return before, applied, nil
}

// SystemProxyStatus 只返回脱敏状态，不暴露备份中的 PAC 地址和原代理配置。
func (s *Service) SystemProxyStatus(ctx context.Context) platform.SystemProxyStatus {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cfg := s.store.Snapshot()
	current, err := s.systemProxy.Capture(ctx)
	if err != nil {
		return platform.SystemProxyStatus{State: "unknown", Message: err.Error(), RecoveryPending: cfg.SystemProxy.Lease != nil}
	}
	status := s.systemProxy.Inspect(current, fmt.Sprintf("127.0.0.1:%d", cfg.Mihomo.MixedPort))
	if lease := cfg.SystemProxy.Lease; lease != nil {
		before, applied, err := decodeProxyLease(lease)
		if err != nil {
			status.RecoveryPending = true
			status.Message += " · 备份损坏，请勿手动删除"
			return status
		}
		_, conflicts, planErr := platform.ProxyRestorePlan(current, before, applied, false)
		status.Conflict = planErr != nil || len(conflicts) > 0 || lease.Phase == "conflict"
		status.Managed = lease.Phase == "active" && !status.Conflict && status.State == "this_app"
		status.RecoveryPending = !status.Managed
		if status.Managed {
			status.Message += " · 本程序管理"
		} else if status.Conflict {
			status.Message += " · 外部修改，原设置备份保留"
		} else {
			status.Message += " · 有待恢复备份"
		}
	}
	return status
}

func (s *Service) waitProxyPort(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	address := fmt.Sprintf("127.0.0.1:%d", s.store.Snapshot().Mihomo.MixedPort)
	for {
		if s.core.Status(ctx).State != core.StateRunning {
			return errors.New("代理内核未运行，不修改系统代理；请先 /core start")
		}
		dialer := net.Dialer{Timeout: 200 * time.Millisecond}
		if connection, err := dialer.DialContext(ctx, "tcp", address); err == nil {
			connection.Close()
			return nil
		}
		timer := time.NewTimer(80 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("代理端口尚未监听，系统代理未接管；请查看 /logs")
		case <-timer.C:
		}
	}
}

// Connect 是事务式连接：内核/端口就绪后才改系统设置；设置失败时回滚本次新启动的内核。
// 外网检测失败不会偷偷关闭已建立的连接，而是返回明确的警告与逐路径结果。
func (s *Service) Connect(ctx context.Context, options ConnectOptions) (ProxyOperationResult, error) {
	if options.Adopt && options.Replace {
		return ProxyOperationResult{}, errors.New("--adopt 与 --replace 不能同时使用")
	}
	if !s.subscriptionAction.TryLock() {
		return ProxyOperationResult{}, errors.New("内核或订阅操作正在执行，请稍后连接")
	}
	defer s.subscriptionAction.Unlock()
	previous := s.store.Snapshot()
	initial := s.core.Status(ctx).State
	if initial == core.StateNotInstalled {
		return ProxyOperationResult{}, errors.New("尚未安装内核，请先 /install mihomo")
	}
	if initial == core.StateStarting || initial == core.StateStopping {
		return ProxyOperationResult{}, errors.New("内核正在切换状态，请稍后连接")
	}
	started := initial != core.StateRunning
	if started {
		if err := s.core.Start(ctx); err != nil {
			return ProxyOperationResult{}, err
		}
	}
	result, err := s.enableSystemProxyLocked(ctx, options)
	if err != nil {
		if started {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 回滚失败且设置仍可能指向本程序时，保持内核运行，避免制造死端口。
			if s.store.Snapshot().SystemProxy.Lease == nil {
				err = errors.Join(err, s.core.Stop(cleanup), s.store.Update(func(cfg *config.Config) error { cfg.Mihomo.AutoStart = previous.Mihomo.AutoStart; return nil }))
			}
		}
		return result, err
	}
	if !options.SkipCheck {
		report, checkErr := s.CheckConnectivity(ctx)
		if checkErr != nil {
			result.Warning, result.Message = true, "已接入系统代理，但联网检测未完成；请 /proxy check 重试"
		} else {
			result.Connectivity = &report
			conclusion := SummarizeConnection(s.Overview(ctx))
			result.Warning = conclusion.Level != "ok"
			result.Message = conclusion.Title
		}
	}
	return result, nil
}

func (s *Service) EnableSystemProxy(ctx context.Context, options ConnectOptions) (ProxyOperationResult, error) {
	if !s.subscriptionAction.TryLock() {
		return ProxyOperationResult{}, errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	return s.enableSystemProxyLocked(ctx, options)
}

func (s *Service) enableSystemProxyLocked(ctx context.Context, options ConnectOptions) (ProxyOperationResult, error) {
	if options.Adopt && options.Replace {
		return ProxyOperationResult{}, errors.New("--adopt 与 --replace 不能同时使用")
	}
	if err := s.waitProxyPort(ctx); err != nil {
		return ProxyOperationResult{}, err
	}
	unlock, err := s.systemProxy.Lock(ctx)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	defer unlock()
	return s.enableSystemProxyTransaction(ctx, options)
}

// 调用方持有跨进程平台锁；必须先持久化 prepared 日志再执行任何操作系统写入。
func (s *Service) enableSystemProxyTransaction(ctx context.Context, options ConnectOptions) (ProxyOperationResult, error) {
	cfg := s.store.Snapshot()
	endpoint := fmt.Sprintf("127.0.0.1:%d", cfg.Mihomo.MixedPort)
	current, err := s.systemProxy.Capture(ctx)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	if lease := cfg.SystemProxy.Lease; lease != nil {
		if lease.Phase == "conflict" {
			return ProxyOperationResult{}, errors.New("有外部修改的代理备份；请先 /system-proxy recover，必要时显式 --force 恢复")
		}
		before, applied, err := decodeProxyLease(lease)
		if err != nil {
			return ProxyOperationResult{}, err
		}
		_, conflicts, err := platform.ProxyRestorePlan(current, before, applied, false)
		if err != nil || len(conflicts) > 0 {
			return ProxyOperationResult{}, errors.New("系统代理已被其他软件修改；先 /system-proxy recover，不会自动覆盖")
		}
		if lease.Phase == "active" && lease.Endpoint == endpoint && platform.EqualProxySnapshots(current, applied) {
			if err := s.saveProxyPreference(true); err != nil {
				return ProxyOperationResult{}, err
			}
			return ProxyOperationResult{Message: "系统代理已由本程序管理，无需重复接管", SystemProxy: s.SystemProxyStatus(ctx)}, nil
		}
		result, err := s.restoreSystemProxyTransaction(ctx, false)
		if err != nil {
			return result, err
		}
		if result.Warning {
			return result, errors.New("系统代理存在外部修改，备份保留，请先处理恢复")
		}
		current, err = s.systemProxy.Capture(ctx)
		if err != nil {
			return result, err
		}
	}
	status := s.systemProxy.Inspect(current, endpoint)
	before := platform.CloneProxySnapshot(current)
	switch status.State {
	case "this_app":
		if !options.Adopt {
			return ProxyOperationResult{}, errors.New("现有手动代理已指向本程序；使用 /connect --adopt 接管，断开时关闭此手动代理（或先手动关闭）")
		}
		before = s.systemProxy.Disabled(current)
	case "other", "automatic":
		if !options.Replace {
			return ProxyOperationResult{}, errors.New("存在其他代理或 PAC；默认不覆盖，确认后使用 /connect --replace（原设置会备份）")
		}
	case "off":
	default:
		return ProxyOperationResult{}, errors.New("无法确认原系统代理状态，拒绝接管；请 /proxy setup")
	}
	target, err := s.systemProxy.Target(current, endpoint)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	originalJSON, _ := json.Marshal(before)
	targetJSON, _ := json.Marshal(target)
	lease := &config.SystemProxyLease{Before: originalJSON, Applied: targetJSON, Endpoint: endpoint, Phase: "prepared"}
	if err := s.store.Update(func(cfg *config.Config) error { cfg.SystemProxy.Lease = lease; return nil }); err != nil {
		return ProxyOperationResult{}, fmt.Errorf("保存原代理设置失败，系统未改动: %w", err)
	}
	rollback := func(cause error) (ProxyOperationResult, error) {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, restoreErr := s.restoreSystemProxyTransaction(cleanup, false)
		return result, errors.Join(cause, restoreErr)
	}
	if err := s.systemProxy.Apply(ctx, target); err != nil {
		return rollback(err)
	}
	actual, err := s.systemProxy.Capture(ctx)
	if err != nil {
		return rollback(err)
	}
	if !platform.EqualProxySnapshots(actual, target) {
		return rollback(errors.New("系统未接受完整代理设置，已尝试恢复原设置"))
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		cfg.SystemProxy.Lease.Phase = "active"
		cfg.SystemProxy.AutoConnect, cfg.Mihomo.AutoStart = true, true
		return nil
	}); err != nil {
		return rollback(fmt.Errorf("保存接管结果失败: %w", err))
	}
	return ProxyOperationResult{Message: "已接入系统代理；原设置已备份，联网尚需检测", SystemProxy: s.SystemProxyStatus(ctx)}, nil
}

func (s *Service) saveProxyPreference(enabled bool) error {
	return s.store.Update(func(cfg *config.Config) error {
		cfg.SystemProxy.AutoConnect = enabled
		if enabled {
			cfg.Mihomo.AutoStart = true
		}
		return nil
	})
}

func (s *Service) RestoreSystemProxy(ctx context.Context, force bool) (ProxyOperationResult, error) {
	if !s.subscriptionAction.TryLock() {
		return ProxyOperationResult{}, errors.New("内核或订阅操作正在执行")
	}
	defer s.subscriptionAction.Unlock()
	if err := s.saveProxyPreference(false); err != nil {
		return ProxyOperationResult{}, err
	}
	return s.restoreSystemProxyLocked(ctx, force)
}

func (s *Service) restoreSystemProxyLocked(ctx context.Context, force bool) (ProxyOperationResult, error) {
	if s.store.Snapshot().SystemProxy.Lease == nil {
		return ProxyOperationResult{Message: "没有本程序管理的代理设置；保留现有系统设置", SystemProxy: s.SystemProxyStatus(ctx)}, nil
	}
	unlock, err := s.systemProxy.Lock(ctx)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	defer unlock()
	return s.restoreSystemProxyTransaction(ctx, force)
}

func (s *Service) restoreSystemProxyTransaction(ctx context.Context, force bool) (ProxyOperationResult, error) {
	lease := s.store.Snapshot().SystemProxy.Lease
	if lease == nil {
		return ProxyOperationResult{Message: "没有待恢复的代理设置"}, nil
	}
	before, applied, err := decodeProxyLease(lease)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	current, err := s.systemProxy.Capture(ctx)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	plan, conflicts, err := platform.ProxyRestorePlan(current, before, applied, force)
	if err != nil {
		return ProxyOperationResult{}, err
	}
	if len(plan.Entries) > 0 {
		if err := s.systemProxy.Apply(ctx, plan); err != nil {
			return ProxyOperationResult{}, fmt.Errorf("恢复系统代理失败，备份保留，内核不会因此被停止: %w", err)
		}
		actual, err := s.systemProxy.Capture(ctx)
		if err != nil {
			return ProxyOperationResult{}, err
		}
		remaining, remainingConflicts, err := platform.ProxyRestorePlan(actual, before, applied, force)
		if err != nil {
			return ProxyOperationResult{}, err
		}
		if len(remaining.Entries) > 0 {
			return ProxyOperationResult{}, errors.New("系统未完成恢复，备份保留，请 /system-proxy recover 重试")
		}
		conflicts = remainingConflicts
	}
	if len(conflicts) > 0 {
		if err := s.store.Update(func(cfg *config.Config) error {
			cfg.SystemProxy.Lease.Phase = "conflict"
			cfg.SystemProxy.AutoConnect = false
			return nil
		}); err != nil {
			return ProxyOperationResult{}, err
		}
		return ProxyOperationResult{Message: "系统代理已被外部修改：保留现状，不覆盖；原设置备份保留，/system-proxy recover 可检查恢复", Warning: true, SystemProxy: s.SystemProxyStatus(ctx)}, nil
	}
	if err := s.store.Update(func(cfg *config.Config) error { cfg.SystemProxy.Lease = nil; return nil }); err != nil {
		return ProxyOperationResult{}, err
	}
	return ProxyOperationResult{Message: "本程序管理的系统代理已恢复原设置", SystemProxy: s.SystemProxyStatus(ctx)}, nil
}

func (s *Service) Disconnect(ctx context.Context) (ProxyOperationResult, error) {
	if !s.subscriptionAction.TryLock() {
		return ProxyOperationResult{}, errors.New("内核或订阅操作正在执行，请稍后断开")
	}
	defer s.subscriptionAction.Unlock()
	result, err := s.restoreSystemProxyLocked(ctx, false)
	if err != nil {
		return result, err
	}
	if err := s.core.Stop(ctx); err != nil {
		return result, err
	}
	if err := s.store.Update(func(cfg *config.Config) error {
		cfg.Mihomo.AutoStart, cfg.SystemProxy.AutoConnect = false, false
		return nil
	}); err != nil {
		return result, err
	}
	result.Message += "；代理内核已停止"
	return result, nil
}

// SafeShutdown 用于关闭后台：恢复当前接管、停止内核，但保留正常情况下的下次连接偏好。
func (s *Service) SafeShutdown(ctx context.Context) error {
	for !s.subscriptionAction.TryLock() {
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("内核或订阅操作尚未完成，代理备份保留，请稍后关闭后台")
		case <-timer.C:
		}
	}
	defer s.subscriptionAction.Unlock()
	if _, err := s.restoreSystemProxyLocked(ctx, false); err != nil {
		return err
	}
	if s.core.Status(ctx).State != core.StateStopped && s.core.Status(ctx).State != core.StateNotInstalled {
		return s.core.Stop(ctx)
	}
	return nil
}

// Resume 在后台重新启动时先处理上次未完成的事务，再按保存偏好恢复连接。
// 不使用 --replace/--adopt，避免开机时悄悄接管其他软件的新设置。
func (s *Service) Resume(ctx context.Context) error {
	preferences := s.store.Snapshot()
	if s.store.Snapshot().SystemProxy.Lease != nil {
		if !s.subscriptionAction.TryLock() {
			return errors.New("启动恢复与其他操作冲突")
		}
		result, err := s.restoreSystemProxyLocked(ctx, false)
		s.subscriptionAction.Unlock()
		if err != nil {
			return err
		}
		if result.Warning {
			return errors.New(result.Message)
		}
	}
	if preferences.SystemProxy.AutoConnect {
		_, err := s.Connect(ctx, ConnectOptions{SkipCheck: true})
		return err
	}
	if preferences.Mihomo.AutoStart && s.core.Status(ctx).State != core.StateNotInstalled {
		return s.StartCore(ctx)
	}
	return nil
}
