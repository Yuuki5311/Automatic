package catslider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

var errSliderEscalated = errors.New("交易猫滑块验证失败")

func failIfContextDone(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return failIfContextDone(ctx)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomIntInRange(min, max int) (int, error) {
	if min > max {
		min, max = max, min
	}
	if min == max {
		return min, nil
	}
	return min + rand.Intn(max-min+1), nil
}

// ReadSliderRect 读取滑块弹窗的 bounding box。
func ReadSliderRect(sess *Session) (SliderRect, bool, error) {
	if sess == nil {
		return SliderRect{}, false, nil
	}
	err, result := sess.JavaScript(sliderRectJS)
	if err != nil {
		return SliderRect{}, false, err
	}
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "undefined" || trimmed == "null" {
		return SliderRect{}, false, nil
	}
	var rect SliderRect
	if err := json.Unmarshal([]byte(trimmed), &rect); err != nil {
		return SliderRect{}, false, err
	}
	if rect.Width <= 0 || rect.Height <= 0 {
		return SliderRect{}, false, nil
	}
	return rect, true, nil
}

func readSliderProbe(sess *Session) (Probe, error) {
	if sess == nil {
		return Probe{}, nil
	}
	err, result := sess.JavaScript(sliderProbeJS)
	if err != nil {
		return Probe{}, err
	}
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "undefined" || trimmed == "null" {
		return Probe{}, nil
	}
	var probe Probe
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return Probe{}, err
	}
	return probe, nil
}

func resolveDragMetrics(sess *Session, rect SliderRect, rng *rand.Rand) (DragMetrics, error) {
	if rect.Width <= 0 || rect.Height <= 0 {
		return DragMetrics{}, fmt.Errorf("交易猫滑块位置信息无效")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	probe, err := readSliderProbe(sess)
	if err != nil {
		return DragMetrics{}, err
	}
	if probe.Escalated {
		message := strings.TrimSpace(probe.FailureText)
		if message == "" {
			message = "滑块验证失败，可能已触发风控升级"
		}
		return DragMetrics{}, fmt.Errorf("%w: %s", errSliderEscalated, message)
	}
	if probe.Mode == "dynamic" && probe.HasHandle && probe.Distance >= 120 {
		distance := probe.Distance + float64(rng.Intn(9)-4)
		if distance < 120 {
			distance = probe.Distance
		}
		return DragMetrics{StartX: probe.StartX, StartY: probe.StartY, Distance: distance, Mode: "dynamic"}, nil
	}
	startX := float64(rect.X + 83)
	startY := float64(rect.Y + 215)
	distance := float64(rect.Width - 96)
	if distance < 180 {
		distance = 180
	}
	if distance > 300 {
		distance = 300
	}
	distance += float64(rng.Intn(16) - 8)
	if distance < 160 {
		distance = 160
	}
	return DragMetrics{StartX: startX, StartY: startY, Distance: distance, Mode: "fallback"}, nil
}

func failIfSliderEscalated(sess *Session, phase string, cycle, attempt int) error {
	probe, err := readSliderProbe(sess)
	if err != nil {
		return err
	}
	if !probe.Escalated {
		return nil
	}
	message := strings.TrimSpace(probe.FailureText)
	if message == "" {
		message = "滑块验证失败，可能已触发风控升级"
	}
	logPhase(sess, "abort", cycle, attempt, "source=%s escalated=true text=%s", strings.TrimSpace(phase), message)
	return fmt.Errorf("%w: %s", errSliderEscalated, message)
}

func failIfPhaseExpired(ctx context.Context, phaseStart time.Time, phaseTimeout time.Duration) error {
	if err := failIfContextDone(ctx); err != nil {
		return err
	}
	if phaseStart.IsZero() {
		return nil
	}
	if phaseTimeout <= 0 {
		phaseTimeout = PhaseTimeout
	}
	if time.Since(phaseStart) > phaseTimeout {
		return fmt.Errorf("交易猫滑块阶段超过 %s", phaseTimeout)
	}
	return nil
}

func logPhase(sess *Session, phase string, cycle, attempt int, detailFormat string, args ...any) {
	if sess == nil {
		return
	}
	prefix := fmt.Sprintf("交易猫滑块 phase=%s cycle=%d attempt=%d", strings.TrimSpace(phase), cycle, attempt)
	if strings.TrimSpace(detailFormat) == "" {
		sess.LogAction("%s", prefix)
		return
	}
	sess.LogAction(prefix+" "+strings.TrimSpace(detailFormat), args...)
}

// ReadToastMessage 读取交易猫页面吐司提示。
func ReadToastMessage(sess *Session) (string, bool, error) {
	if sess == nil {
		return "", false, nil
	}
	err, result := sess.JavaScript(`return String((function () {
		let list = document.querySelectorAll("div")
		for (let i = 0; i < list.length; i++) {
			let str = list[i].role
			let t = list[i].textContent
			if(str && str == "alert" && t){
				return t
			}
		}
	})() || "")`)
	if err != nil {
		return "", false, err
	}
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "undefined" || trimmed == "null" {
		return "", false, nil
	}
	return trimmed, true, nil
}

func attemptSliderSolve(ctx context.Context, sess *Session, rect SliderRect, phase string, cycle, attempt int) error {
	if err := failIfContextDone(ctx); err != nil {
		return err
	}
	if err := failIfSliderEscalated(sess, phase, cycle, attempt); err != nil {
		return err
	}
	if err := SolveSlider(ctx, sess, rect, cycle, attempt); err != nil {
		return err
	}
	if err := sess.WaitStable(800 * time.Millisecond); err != nil {
		sess.LogAction("交易猫%s滑块拖动后等待稳定失败 err=%v", strings.TrimSpace(phase), err)
	}
	return failIfSliderEscalated(sess, phase, cycle, attempt)
}
