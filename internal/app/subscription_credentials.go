package app

import (
	"context"
	"errors"
	"strings"

	"github.com/itrunswap/Kivo/internal/config"
)

// SubscriptionSavedError 表示持久化已经成功，但运行中的内核未加载新配置。
// HTTP 将其转换为带 warning 的成功响应，避免编辑页保留“未保存”假象或重复创建。
type SubscriptionSavedError struct{ Cause error }

func (e *SubscriptionSavedError) Error() string {
	return "订阅配置已保存，但内核加载失败；请检查日志并重试重启内核，不要重复添加订阅"
}
func (e *SubscriptionSavedError) Unwrap() error { return e.Cause }
func (s *Service) reloadSavedSubscription(ctx context.Context) error {
	if err := s.reloadIfRunning(ctx); err != nil {
		return &SubscriptionSavedError{Cause: err}
	}
	return nil
}

// SubscriptionCredentialPatch 不返回已有凭据；省略表示保留，显式 none 表示清除。
// 密码不 TrimSpace，避免改变用户真实口令中的空格。
type SubscriptionCredentialPatch struct {
	Type     *string `json:"type,omitempty"`
	Username *string `json:"username,omitempty"`
	Secret   *string `json:"secret,omitempty"`
}

func normalizeSubscriptionCredentials(auth *config.SubscriptionAuth, decrypt *config.SubscriptionDecryption) error {
	auth.Type = strings.ToLower(strings.TrimSpace(auth.Type))
	decrypt.Type = strings.ToLower(strings.TrimSpace(decrypt.Type))
	if auth.Type == "" {
		auth.Type = "none"
	}
	if decrypt.Type == "" {
		decrypt.Type = "none"
	}
	if auth.Type == "aes" || auth.Type == "age" {
		return errors.New("下载认证不能使用解密类型，请在内容解密中设置")
	}
	if auth.Type == "none" {
		auth.Username, auth.Secret = "", ""
	}
	if auth.Type != "basic" {
		auth.Username = ""
	}
	if decrypt.Type == "none" {
		decrypt.Secret = ""
	}
	return nil
}

func patchSubscriptionCredentials(sub *config.Subscription, patch SubscriptionPatch) error {
	legacy := patch.AuthType != nil || patch.Username != nil || patch.Secret != nil
	modern := patch.DownloadAuth != nil || patch.Decryption != nil
	if legacy && modern {
		return errors.New("不能同时提交旧版认证字段和分层凭据")
	}
	if !legacy && !modern {
		return nil
	}
	auth, decrypt := sub.Credentials()
	if legacy {
		// 旧 CLI/Web 只描述一种凭据。仅修改旧 secret 时定位到原有层；
		// 同时存在两层时拒绝旧写入，防止静默丢失另一层秘密。
		if auth.Type != "none" && decrypt.Type != "none" {
			return errors.New("此订阅同时使用下载认证和解密，请使用新版桌面编辑或分层 API 修改凭据")
		}
		value := auth
		if decrypt.Type != "none" {
			value = config.SubscriptionAuth{Type: decrypt.Type, Secret: decrypt.Secret}
		}
		if patch.AuthType != nil {
			kind := strings.ToLower(strings.TrimSpace(*patch.AuthType))
			if kind != value.Type {
				value = config.SubscriptionAuth{Type: kind}
			}
		}
		if patch.Username != nil {
			value.Username = *patch.Username
		}
		if patch.Secret != nil {
			value.Secret = *patch.Secret
		}
		if value.Type == "none" {
			value = config.SubscriptionAuth{Type: "none"}
		}
		sub.Auth, sub.Decryption = value, config.SubscriptionDecryption{}
		return nil
	}
	if p := patch.DownloadAuth; p != nil {
		if p.Type != nil && *p.Type != auth.Type {
			auth = config.SubscriptionAuth{Type: *p.Type}
		}
		if p.Username != nil {
			auth.Username = *p.Username
		}
		if p.Secret != nil {
			auth.Secret = *p.Secret
		}
	}
	if p := patch.Decryption; p != nil {
		if p.Type != nil && *p.Type != decrypt.Type {
			decrypt = config.SubscriptionDecryption{Type: *p.Type}
		}
		if p.Secret != nil {
			decrypt.Secret = *p.Secret
		}
	}
	if err := normalizeSubscriptionCredentials(&auth, &decrypt); err != nil {
		return err
	}
	sub.Auth, sub.Decryption = auth, decrypt
	return nil
}
