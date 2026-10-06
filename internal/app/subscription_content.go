package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/subscription"
)

const maxEncryptedSubscriptionSize = 32 << 20

type subscriptionDownloadResult struct {
	name, via  string
	err        error
	finishedAt time.Time
}

// DecryptedSubscriptionContent 下载并解密指定的 AES 订阅。该方法只由受保护的
// 内部 provider 端点调用，返回值不得写入日志或普通管理 API。
func (s *Service) DecryptedSubscriptionContent(ctx context.Context, name string) (content []byte, resultErr error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("缺少订阅名称")
	}
	cfg := s.store.Snapshot()
	var targetURL, password, updateVia string
	for _, item := range cfg.Subscriptions {
		if strings.EqualFold(item.Name, name) {
			if !strings.EqualFold(item.Auth.Type, "aes") {
				return nil, fmt.Errorf("订阅 %s 不是 AES 加密订阅", item.Name)
			}
			targetURL, password, updateVia = item.URL, item.Auth.Secret, item.UpdateVia
			break
		}
	}
	if targetURL == "" {
		return nil, fmt.Errorf("订阅 %s 不存在", name)
	}
	// Mihomo 可能只透传内部端点的 HTTP 状态码。保留本次安全错误，让 CLI/Web
	// 能分辨上游 HTTP 失败、解密失败和代理不可用；不保存 URL、密码或响应正文。
	defer func() {
		via := "直连"
		if strings.EqualFold(updateVia, "proxy") {
			via = "PROXY 出站"
		}
		s.downloadMu.Lock()
		s.lastDownload = subscriptionDownloadResult{name: name, via: via, err: resultErr, finishedAt: time.Now()}
		s.downloadMu.Unlock()
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		// URL 可能携带订阅 Token，不把底层解析错误原样传到 API 或日志。
		return nil, errors.New("创建 AES 订阅下载请求失败，请检查订阅地址")
	}
	request.Header.Set("Accept", "*/*")
	// 与普通 Mihomo provider 保持一致，强制订阅站返回 Clash/Mihomo 格式。
	request.Header.Set("User-Agent", "Clash.Meta")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	// 直连模式显式忽略 HTTP_PROXY/HTTPS_PROXY，确保命令语义不受外部环境变量影响。
	transport.Proxy = nil
	if strings.EqualFold(strings.TrimSpace(updateVia), "proxy") {
		provider, ok := s.core.(core.SubscriptionProxy)
		if !ok {
			return nil, errors.New("当前内核不支持固定代理下载，请使用 --direct")
		}
		proxyURL, err := provider.SubscriptionProxyURL(ctx)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	client := &http.Client{Timeout: 45 * time.Second, Transport: transport}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// net/http 的错误通常包含完整 URL，因此这里只返回不含敏感地址的说明。
		return nil, errors.New("下载 AES 订阅失败，请检查网络、DNS 和订阅地址")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("下载 AES 订阅返回 HTTP %d", response.StatusCode)
	}
	encrypted, err := io.ReadAll(io.LimitReader(response.Body, maxEncryptedSubscriptionSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取 AES 订阅: %w", err)
	}
	if len(encrypted) > maxEncryptedSubscriptionSize {
		return nil, fmt.Errorf("AES 订阅超过 %d MiB 限制", maxEncryptedSubscriptionSize>>20)
	}
	plaintext, err := subscription.DecryptAES(encrypted, password)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(plaintext))) == 0 {
		return nil, errors.New("AES 订阅解密结果为空")
	}
	return plaintext, nil
}
