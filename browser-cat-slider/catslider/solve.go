package catslider

// solve.go：滑块拖动入口（轨迹回放或 easing 算法），在全局锁内执行。

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// SolveSlider 模拟人工拖动交易猫滑块（含全局锁）。
func SolveSlider(ctx context.Context, sess *Session, rect SliderRect, cycle, attempt int) error {
	return WithSliderLock(ctx, func() error {
		return solveSliderLocked(ctx, sess, rect, cycle, attempt)
	})
}

func solveSliderLocked(ctx context.Context, sess *Session, rect SliderRect, cycle, attempt int) error {
	page, err := sess.MainPage()
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(cycle*100+attempt)))
	metrics, err := resolveDragMetrics(sess, rect, rng)
	if err != nil {
		return err
	}
	cooldown := time.Duration(1000+rng.Intn(2001)) * time.Millisecond
	logPhase(sess, "cooldown", cycle, attempt,
		"wait=%dms before drag mode=%s start=(%.2f,%.2f) distance=%.2f",
		cooldown.Milliseconds(), metrics.Mode, metrics.StartX, metrics.StartY, metrics.Distance)
	if err := sleepContext(ctx, cooldown); err != nil {
		return err
	}
	traces, traceErr := getSliderTraces()
	if traceErr == nil && len(traces) > 0 {
		return solveWithTrace(ctx, sess, page, rect, cycle, attempt, metrics.StartX, metrics.StartY, metrics.Distance, traces, rng)
	}
	if traceErr != nil {
		logPhase(sess, "plan", cycle, attempt, "trace_fallback reason=%v", traceErr)
	}
	return solveWithEasing(ctx, sess, page, rect, cycle, attempt, metrics.StartX, metrics.StartY, metrics.Distance, rng)
}

func solveWithTrace(ctx context.Context, sess *Session, page *rod.Page, rect SliderRect, cycle, attempt int,
	startX, startY, distance float64, traces []sliderTrace, rng *rand.Rand) error {
	trace, err := pickTrace(traces, rng)
	if err != nil {
		return err
	}
	plan, err := trace.buildDragPlan(startX, startY, distance, rng)
	if err != nil {
		logPhase(sess, "plan", cycle, attempt, "trace_build_failed source=%s err=%v", trace.Source, err)
		return solveWithEasing(ctx, sess, page, rect, cycle, attempt, startX, startY, distance, rng)
	}
	logPhase(sess, "plan", cycle, attempt,
		"mode=trace source=%s start=(%.2f,%.2f) distance=%.2f target=%.2f steps=%d rect=%+v",
		plan.SourceTrace, startX, startY, distance, plan.TargetDist, len(plan.Steps), rect)
	if err := replayDragPlan(ctx, page, plan, TraceMaxDuration, dragReplayHooks{
		OnDown: func(step replayStep) {
			logPhase(sess, "drag", cycle, attempt, "down point=(%.2f,%.2f)", step.X, step.Y)
		},
		OnStep: func(index, total int, step replayStep) {
			logPhase(sess, "step", cycle, attempt, "step=%d/%d point=(%.2f,%.2f)", index, total, step.X, step.Y)
		},
		OnRelease: func(step replayStep) {
			logPhase(sess, "release", cycle, attempt, "point=(%.2f,%.2f)", step.X, step.Y)
		},
	}); err != nil {
		return err
	}
	return settleAfterDrag(ctx, sess, cycle, attempt)
}

func solveWithEasing(ctx context.Context, sess *Session, page *rod.Page, rect SliderRect, cycle, attempt int,
	startX, startY, distance float64, rng *rand.Rand) error {
	steps := 18 + rng.Intn(10)
	mousePage := timedPage(page)
	start := proto.NewPoint(startX, startY)
	if err := mousePage.Mouse.MoveTo(start); err != nil {
		return err
	}
	if err := mousePage.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	waitTime := time.Duration(500+rng.Intn(2500)) * time.Millisecond
	logPhase(sess, "plan", cycle, attempt, "mode=easing start=(%.2f,%.2f) distance=%.2f steps=%d wait=%s rect=%+v",
		startX, startY, distance, steps, waitTime, rect)
	if err := sleepContext(ctx, waitTime); err != nil {
		return err
	}
	if err := mousePage.Mouse.MoveTo(start); err != nil {
		return err
	}
	if err := mousePage.Mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	overshoot := rng.Intn(100) < 35
	targetX := startX + distance
	if overshoot {
		targetX += float64(rng.Intn(14) + 5)
	}
	targetY := startY + float64(rng.Intn(7)-3)
	for step := 1; step <= steps; step++ {
		if err := failIfContextDone(ctx); err != nil {
			_ = mousePage.Mouse.Up(proto.InputMouseButtonLeft, 1)
			return err
		}
		progress := float64(step) / float64(steps)
		eased := 1 - math.Pow(1-progress, 3)
		x := startX + (targetX-startX)*eased
		y := startY + (targetY-startY)*eased
		y += math.Sin(progress*math.Pi) * float64(rng.Intn(5)-2)
		if step > steps-3 && overshoot {
			x = targetX - float64(rng.Intn(4))
		}
		if err := mousePage.Mouse.MoveTo(proto.NewPoint(x, y)); err != nil {
			_ = mousePage.Mouse.Up(proto.InputMouseButtonLeft, 1)
			return err
		}
		if err := sleepContext(ctx, time.Duration(10+rng.Intn(25))*time.Millisecond); err != nil {
			_ = mousePage.Mouse.Up(proto.InputMouseButtonLeft, 1)
			return err
		}
	}
	if err := mousePage.Mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	return settleAfterDrag(ctx, sess, cycle, attempt)
}

func settleAfterDrag(ctx context.Context, sess *Session, cycle, attempt int) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	waitAfter := time.Duration(1000+rng.Intn(5001)) * time.Millisecond
	logPhase(sess, "settle", cycle, attempt, "wait=%s", waitAfter)
	if err := sleepContext(ctx, waitAfter); err != nil {
		return err
	}
	if failText, ok, err := ReadToastMessage(sess); err == nil && ok && strings.TrimSpace(failText) != "" {
		logPhase(sess, "result", cycle, attempt, "status=fail text=%s", strings.TrimSpace(failText))
		return nil
	}
	logPhase(sess, "result", cycle, attempt, "status=pass")
	return nil
}
