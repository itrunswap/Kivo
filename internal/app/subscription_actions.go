package app

import (
	"context"
	"fmt"
	"time"

	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

// SubscriptionActionItem 记录每个订阅的真实结果；nil 数量表示未知，不冒充 0。
// 节点数是解析结果，不代表全部在线，也不等同于新增节点数。
type SubscriptionActionItem struct {
	Name          string     `json:"name"`
	Via           string     `json:"via"`
	NodeCount     *int       `json:"nodeCount,omitempty"`
	PreviousCount *int       `json:"previousCount,omitempty"`
	DurationMS    int64      `json:"durationMs"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
	Error         string     `json:"error,omitempty"`
	Warning       string     `json:"warning,omitempty"`
}

func (s *Service) performSubscriptionAction(ctx context.Context, sub config.Subscription, via string, tester core.ProviderHealth) (item SubscriptionActionItem, err error) {
	item.Name, item.Via = sub.Name, defaultString(via, defaultString(sub.UpdateVia, "direct"))
	started := time.Now()
	defer func() {
		item.DurationMS = time.Since(started).Milliseconds()
		if err != nil {
			item.Error = err.Error()
		}
	}()
	reporter, reports := s.core.(core.ProviderReporter)
	if reports {
		if before, snapshotErr := reporter.SubscriptionSnapshot(ctx, sub.Name); snapshotErr == nil {
			count := len(before.Nodes)
			item.PreviousCount = &count
		}
	}
	if err = s.UpdateSubscription(ctx, sub.Name); err != nil {
		return item, err
	}
	if reports {
		after, snapshotErr := reporter.SubscriptionSnapshot(ctx, sub.Name)
		if snapshotErr != nil {
			return item, fmt.Errorf("订阅刷新完成但无法确认节点数: %w", snapshotErr)
		}
		count := len(after.Nodes)
		item.NodeCount = &count
		if !after.UpdatedAt.IsZero() {
			item.UpdatedAt = &after.UpdatedAt
		}
	} else {
		item.Warning = "当前内核未提供节点统计"
	}
	if tester != nil {
		err = tester.TestSubscription(ctx, sub.Name)
	}
	return item, err
}
