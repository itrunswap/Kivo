package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SubscriptionDecryption 与 HTTP 认证相互独立。Secret 只用于本地解密，
// 绝不能因为格式识别失败而尝试把它作为 HTTP 密码发送给远端。
type SubscriptionDecryption struct {
	Type   string `json:"type,omitempty"` // none、aes、age；AES 是明确的兼容格式，不猜测任意密文。
	Secret string `json:"secret,omitempty"`
}

// SubscriptionOptions 分别保存下载参数、节点筛选与节点覆盖。
// 覆盖值为空/default 时不写入内核配置，保留订阅原始值；on/off 才显式覆盖。
type SubscriptionOptions struct {
	UserAgent      string `json:"userAgent,omitempty"`
	Filter         string `json:"filter,omitempty"`
	ExcludeFilter  string `json:"excludeFilter,omitempty"`
	UDP            string `json:"udp,omitempty"`
	TFO            string `json:"tfo,omitempty"`
	SkipCertVerify string `json:"skipCertVerify,omitempty"`
}

// Credentials 无损解释旧版 Auth 中的 AES/age，读取时不写盘，也不丢弃旧凭据。
// 新编辑页保存时再将两层凭据写成明确、独立的结构。
func (s Subscription) Credentials() (SubscriptionAuth, SubscriptionDecryption) {
	auth, decrypt := s.Auth, s.Decryption
	auth.Type = strings.ToLower(strings.TrimSpace(auth.Type))
	decrypt.Type = strings.ToLower(strings.TrimSpace(decrypt.Type))
	if auth.Type == "aes" || auth.Type == "age" {
		if decrypt.Type == "" {
			decrypt = SubscriptionDecryption{Type: auth.Type, Secret: auth.Secret}
		}
		auth = SubscriptionAuth{Type: "none"}
	}
	if auth.Type == "" {
		auth.Type = "none"
	}
	if decrypt.Type == "" {
		decrypt.Type = "none"
	}
	return auth, decrypt
}

// Revision 用于编辑冲突检测。只返回配置摘要，不向页面回传真实 URL 或凭据。
func (s Subscription) Revision() string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validateSubscriptionOptions(s Subscription) error {
	auth, decrypt := s.Credentials()
	if !slices.Contains([]string{"none", "basic", "bearer", "token"}, auth.Type) {
		return fmt.Errorf("下载认证方式无效")
	}
	if !slices.Contains([]string{"none", "aes", "age"}, decrypt.Type) {
		return fmt.Errorf("内容解密格式无效")
	}
	if decrypt.Type != "none" && strings.TrimSpace(decrypt.Secret) == "" {
		return fmt.Errorf("内容解密需要密码或 age 私钥")
	}
	for _, value := range []string{s.Options.UDP, s.Options.TFO, s.Options.SkipCertVerify} {
		if !slices.Contains([]string{"", "default", "on", "off"}, value) {
			return fmt.Errorf("节点覆盖必须为 default、on 或 off")
		}
	}
	if len(s.Options.UserAgent) > 512 || strings.ContainsAny(s.Options.UserAgent, "\r\n\x00") {
		return fmt.Errorf("User-Agent 过长或包含非法控制字符")
	}
	for _, pattern := range []string{s.Options.Filter, s.Options.ExcludeFilter} {
		if len(pattern) > 2048 {
			return fmt.Errorf("节点过滤表达式不能超过 2048 字节")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("节点过滤表达式无效，请使用 Go/RE2 语法（不支持前后查找）")
		}
	}
	return nil
}
