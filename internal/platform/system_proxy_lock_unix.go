//go:build darwin || linux

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func lockSystemProxy(ctx context.Context) (func(), error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	// 与旧版共用事务锁；改产品名称不能让两个进程并行写入同一系统代理。
	directory := filepath.Join(base, "ProxyPilot")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(directory, "system-proxy.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开用户系统代理事务锁: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			syscall.Close(fd)
			return nil, err
		}
		if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() { syscall.Flock(fd, syscall.LOCK_UN); syscall.Close(fd) }, nil
		} else if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			syscall.Close(fd)
			return nil, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			syscall.Close(fd)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
