package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/itrunswap/Kivo/internal/config"
)

// ErrSettingsConflict 提醒客户端保留草稿，先读取其他客户端已保存的版本。
var ErrSettingsConflict = errors.New("设置已被其他窗口或 CLI 修改，请保留当前输入并重新读取设置后再保存")

// SettingsPatch 通过指针区分“未提交”和 false / 空字符串，避免模式切换覆盖其他设置。
type SettingsPatch struct {
	Revision      string  `json:"revision,omitempty"`
	Listen        *string `json:"listen,omitempty"` // 兼容公开设置中的只读监听地址，不修改监听器。
	MixedPort     *int    `json:"mixedPort,omitempty"`
	Mode          *string `json:"mode,omitempty"`
	AllowLAN      *bool   `json:"allowLAN,omitempty"`
	TUNEnabled    *bool   `json:"tunEnabled,omitempty"`
	DownloadProxy *string `json:"downloadProxy,omitempty"`
	DownloadRetry *int    `json:"downloadRetry,omitempty"`
}

func publicSettings(cfg config.Config) Settings {
	value := Settings{Listen: cfg.Web.Listen, MixedPort: cfg.Mihomo.MixedPort, Mode: cfg.Mihomo.Mode, AllowLAN: cfg.Mihomo.AllowLAN, TUNEnabled: cfg.Mihomo.TUNEnabled, DownloadProxy: cfg.Mihomo.DownloadProxy, DownloadRetry: cfg.Mihomo.DownloadRetry}
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	value.Revision = hex.EncodeToString(hash[:])
	return value
}

// PatchSettings 在事务锁内按最新配置合并；带版本的表单提交采用乐观并发校验。
func (s *Service) PatchSettings(ctx context.Context, patch SettingsPatch) error {
	if !s.subscriptionAction.TryLock() {
		return errors.New("内核或订阅操作正在执行，请稍后修改设置")
	}
	defer s.subscriptionAction.Unlock()
	cfg := s.store.Snapshot()
	value := publicSettings(cfg)
	if patch.Revision != "" && patch.Revision != value.Revision {
		return ErrSettingsConflict
	}
	if patch.MixedPort != nil {
		value.MixedPort = *patch.MixedPort
	}
	if patch.Mode != nil {
		value.Mode = *patch.Mode
	}
	if patch.AllowLAN != nil {
		value.AllowLAN = *patch.AllowLAN
	}
	if patch.TUNEnabled != nil {
		value.TUNEnabled = *patch.TUNEnabled
	}
	if patch.DownloadProxy != nil {
		value.DownloadProxy = *patch.DownloadProxy
	}
	if patch.DownloadRetry != nil {
		if *patch.DownloadRetry < 1 || *patch.DownloadRetry > 10 {
			return errors.New("downloadRetry 必须在 1-10 之间")
		}
		value.DownloadRetry = *patch.DownloadRetry
	}
	return s.updateSettingsLocked(ctx, value, cfg)
}
