package catslider

// lock.go：全局滑块互斥锁。
//
// 多店铺/多 goroutine 同时拖动会污染 isg 等风控 Cookie，因此实际拖动必须在锁内串行执行。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var sliderMu sync.Mutex

// WithSliderLock 全局串行化交易猫滑块拖动，避免多店铺/多协程同时过滑块导致风控 Cookie 冲突。
func WithSliderLock(ctx context.Context, fn func() error) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(LockPollInterval)
	defer ticker.Stop()
	waitDeadline := time.Now().Add(LockMaxWait)
	for {
		if err := ctx.Err(); err != nil {
			return sliderLockWaitError(err)
		}
		if time.Now().After(waitDeadline) {
			return fmt.Errorf("等待交易猫滑块锁超过 %s", LockMaxWait)
		}
		if sliderMu.TryLock() {
			if err := ensureTaskRemainingForSlider(ctx); err != nil {
				sliderMu.Unlock()
				return err
			}
			defer sliderMu.Unlock()
			return fn()
		}
		select {
		case <-ctx.Done():
			return sliderLockWaitError(ctx.Err())
		case <-ticker.C:
		}
	}
}

func ensureTaskRemainingForSlider(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("任务超时，无法完成交易猫滑块验证")
		}
		return sliderLockWaitError(err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return fmt.Errorf("任务超时，无法完成交易猫滑块验证")
	}
	if remaining < MinRemainingBeforeDrag {
		return fmt.Errorf("任务剩余时间不足，无法完成交易猫滑块验证")
	}
	return nil
}

func sliderLockWaitError(err error) error {
	if err == nil {
		return fmt.Errorf("等待交易猫滑块锁超时")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("任务超时，等待交易猫滑块锁失败: %w", err)
	}
	return fmt.Errorf("等待交易猫滑块锁超时: %w", err)
}
