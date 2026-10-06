package core

import (
	"sync"
	"time"
)

// LogBuffer 是有容量上限的并发安全日志环，防止长期运行无限占用内存。
type LogBuffer struct {
	mu       sync.RWMutex
	lines    []string
	capacity int
}

// NewLogBuffer 创建日志环。
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity < 100 {
		capacity = 100
	}
	return &LogBuffer{capacity: capacity, lines: make([]string, 0, capacity)}
}

// Add 添加带时间戳的单行日志。
func (b *LogBuffer) Add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	line = time.Now().Format("15:04:05") + "  " + line
	if len(b.lines) == b.capacity {
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = line
		return
	}
	b.lines = append(b.lines, line)
}

// Last 返回最后 limit 行日志的副本。
func (b *LogBuffer) Last(limit int) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.lines) {
		limit = len(b.lines)
	}
	start := len(b.lines) - limit
	result := make([]string, limit)
	copy(result, b.lines[start:])
	return result
}
