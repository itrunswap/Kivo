package main

import (
	"context"
	"time"

	bridge "github.com/itrunswap/Kivo/internal/desktop"
)

// readOverview 合并页面和托盘的同时读取；变更开始/结束都会使旧快照失效。
// 网络请求在锁外执行，等待者可以独立取消，不阻塞窗口生命周期。
func (d *Desktop) readOverview(ctx context.Context) bridge.Reply {
	for {
		epoch := d.overviewEpoch.Load()
		d.overviewMu.Lock()
		if d.overviewGeneration == epoch && time.Since(d.overviewAt) < 2*time.Second {
			value := d.overviewReply
			d.overviewMu.Unlock()
			return value
		}
		if pending := d.overviewPending; pending != nil {
			d.overviewMu.Unlock()
			select {
			case <-ctx.Done():
				return bridge.Reply{Error: "状态读取已取消"}
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		d.overviewPending = pending
		d.overviewMu.Unlock()
		readCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
		value := d.bridge.Request(readCtx, "GET", "/api/v1/overview", "")
		cancel()
		d.overviewMu.Lock()
		if epoch == d.overviewEpoch.Load() {
			d.overviewGeneration, d.overviewAt, d.overviewReply = epoch, time.Now(), value
		}
		d.overviewPending = nil
		close(pending)
		d.overviewMu.Unlock()
		return value
	}
}
