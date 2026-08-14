package catslider

// retry.go：滑块挑战的统一重试编排。
//
// 弹窗/内嵌：每轮 3 次拖动 → 刷新 → 等 5-10s → 回调 AfterRefresh
// punish 整页：3 次失败后等 2-3 分钟 → 重启浏览器 → 回调 AfterRestart

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// HandleSliderChallenge 统一处理交易猫滑块（弹窗/内嵌/punish 整页）。
func HandleSliderChallenge(ctx context.Context, sess *Session, opts ChallengeOptions) error {
	if sess == nil {
		return nil
	}
	phase := strings.TrimSpace(opts.Phase)
	if phase == "" {
		phase = "滑块"
	}
	rect, visible, err := ReadSliderRect(sess)
	if err != nil {
		return err
	}
	if !visible {
		return nil
	}
	phaseStart := time.Now()
	phaseTimeout := resolvePhaseTimeout(sess)
	logPhase(sess, "detect", 1, 1, "source=%s rect=%+v", phase, rect)

	for refreshCycle := 1; refreshCycle <= RefreshCycleCount; refreshCycle++ {
		if err := failIfPhaseExpired(ctx, phaseStart, phaseTimeout); err != nil {
			return err
		}
		cleared, err := attemptSliderBatch(ctx, sess, phase, refreshCycle)
		if err != nil {
			return err
		}
		if cleared {
			return nil
		}
		mode, err := readSliderMode(sess)
		if err != nil {
			return err
		}
		if mode == ModePunishPage {
			return recoverPunishPageSlider(ctx, sess, opts, phase)
		}
		if refreshCycle >= RefreshCycleCount {
			return fmt.Errorf("交易猫%s滑块连续 %d 轮刷新后仍未通过", phase, RefreshCycleCount)
		}
		sess.LogAction("交易猫%s滑块连续 %d 次仍未通过，刷新页面后重试 cycle=%d/%d mode=%s",
			phase, RetryCount, refreshCycle, RefreshCycleCount, mode)
		if err := sess.Refresh(); err != nil {
			return fmt.Errorf("刷新交易猫%s滑块页面失败: %w", phase, err)
		}
		if err := sleepSliderRefreshWait(ctx, sess, phase, refreshCycle); err != nil {
			return err
		}
		if opts.AfterRefresh != nil {
			if err := opts.AfterRefresh(); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("交易猫%s滑块连续 %d 轮刷新后仍未通过", phase, RefreshCycleCount)
}

func attemptSliderBatch(ctx context.Context, sess *Session, phase string, cycle int) (bool, error) {
	for attempt := 1; attempt <= RetryCount; attempt++ {
		if err := failIfContextDone(ctx); err != nil {
			return false, err
		}
		rect, visible, err := ReadSliderRect(sess)
		if err != nil {
			return false, err
		}
		if !visible {
			return true, nil
		}
		logPhase(sess, "detect", cycle, attempt, "source=%s rect=%+v", phase, rect)
		if err := attemptSliderSolve(ctx, sess, rect, phase, cycle, attempt); err != nil {
			return false, err
		}
	}
	_, visible, err := ReadSliderRect(sess)
	if err != nil {
		return false, err
	}
	return !visible, nil
}

func recoverPunishPageSlider(ctx context.Context, sess *Session, opts ChallengeOptions, phase string) error {
	state := punishRestartStateFromContext(ctx)
	if state != nil && state.Attempted {
		return formatPunishPagePersistentError()
	}
	restartFn := browserRestartFromContext(ctx)
	if restartFn == nil {
		return formatPunishPageError()
	}
	sess.LogAction("交易猫 punish 页%s滑块连续 %d 次仍未通过，准备关闭浏览器并重启", phase, RetryCount)
	if state != nil {
		state.Attempted = true
	}
	seconds, err := randomIntInRange(PunishRestartWaitMinSec, PunishRestartWaitMaxSec)
	if err != nil {
		seconds = PunishRestartWaitMinSec
	}
	waitDuration := time.Duration(seconds) * time.Second
	if !sliderStillBlockingAfterPunishRestart(sess) {
		sess.LogAction("交易猫 punish 页滑块已通过，跳过浏览器重启")
		return nil
	}
	sess.LogAction("交易猫 punish 页等待 %s 后重启浏览器（等待期间若人工过滑块将自动跳过重启）", waitDuration)
	cleared, err := waitForPunishSliderClearOrTimeout(ctx, sess, waitDuration)
	if err != nil {
		return err
	}
	if cleared {
		return nil
	}
	if err := restartFn(ctx); err != nil {
		return fmt.Errorf("交易猫 punish 页重启浏览器失败: %w", err)
	}
	if opts.AfterRestart != nil {
		if err := opts.AfterRestart(); err != nil {
			return err
		}
	}
	if sliderStillBlockingAfterPunishRestart(sess) {
		return formatPunishPagePersistentError()
	}
	return nil
}

func waitForPunishSliderClearOrTimeout(ctx context.Context, sess *Session, wait time.Duration) (bool, error) {
	if wait <= 0 {
		return false, failIfContextDone(ctx)
	}
	deadline := time.Now().Add(wait)
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		if err := failIfContextDone(ctx); err != nil {
			return false, err
		}
		if !sliderStillBlockingAfterPunishRestart(sess) {
			sess.LogAction("交易猫 punish 页滑块已通过，跳过浏览器重启")
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func sliderStillBlockingAfterPunishRestart(sess *Session) bool {
	if sess == nil {
		return false
	}
	if LooksLikePunishURL(currentURL(sess)) {
		return true
	}
	mode, err := readSliderMode(sess)
	if err != nil {
		return false
	}
	return mode == ModePunishPage
}

func sleepSliderRefreshWait(ctx context.Context, sess *Session, phase string, cycle int) error {
	seconds, err := randomIntInRange(RefreshWaitMinSec, RefreshWaitMaxSec)
	if err != nil {
		seconds = RefreshWaitMinSec
	}
	waitDuration := time.Duration(seconds) * time.Second
	sess.LogAction("交易猫%s滑块刷新后等待 cycle=%d wait=%s", strings.TrimSpace(phase), cycle, waitDuration)
	return sleepContext(ctx, waitDuration)
}

// HandleSliderWithRetry 兼容旧调用：刷新/重启浏览器后执行 retryAction。
func HandleSliderWithRetry(ctx context.Context, sess *Session, phase string, retryAction func() error) error {
	return HandleSliderChallenge(ctx, sess, ChallengeOptions{
		Phase:        phase,
		AfterRefresh: retryAction,
		AfterRestart: retryAction,
	})
}
