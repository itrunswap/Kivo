package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
)

// InstallCoreProgress 消费逐行 JSON 安装事件。必须收到 complete 才认为成功；
// 网络断开、下载失败或旧后台服务均不能误报为安装完成。
func (c *Client) InstallCoreProgress(ctx context.Context, version string, progress func(core.InstallEvent)) error {
	data, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/core/install", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	httpClient := *c.http
	httpClient.Timeout = 20 * time.Minute
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接安装服务: %w", err)
	}
	defer resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		var envelope struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Data []core.InstallEvent `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
			return fmt.Errorf("安装服务返回 HTTP %d", resp.StatusCode)
		}
		if envelope.Error != nil {
			return errors.New(envelope.Error.Message)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("安装服务返回 HTTP %d", resp.StatusCode)
		}
		// 老服务已执行安装，不再重新发起；只提示重启后才能获得实时进度。
		if progress != nil {
			progress(core.InstallEvent{Stage: "notice", Message: "后台版本较旧，执行 /web restart 后可显示实时进度"})
			for _, e := range envelope.Data {
				progress(e)
			}
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("安装服务返回 HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 16<<20))
	for {
		var event core.InstallEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("安装进度连接已中断，尚未收到完成确认；可重试以续传")
			}
			return fmt.Errorf("读取安装进度: %w", err)
		}
		if event.Error != "" {
			return errors.New(event.Error)
		}
		if progress != nil {
			progress(event)
		}
		if event.Stage == "complete" {
			return nil
		}
	}
}
