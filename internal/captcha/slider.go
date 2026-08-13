package captcha

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strconv"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
)

var _ Solver = (*SliderSolver)(nil)

var sliderSelectors = []string{
	".slider-button",
	".nc_iconfont.btn_slide",
	".slide-verify-slider",
	"#sliderCaptcha",
	"[class*='slider']",
	"[class*='captcha']",
}

type SliderSolver struct {
	maxRetry       int
	sliderSelector string
	bgImgSelector  string
	gapImgSelector string
}

func NewSliderSolver(cfg *config.CaptchaConfig) *SliderSolver {
	maxRetry := 3
	if cfg != nil && cfg.MaxRetry > 0 {
		maxRetry = cfg.MaxRetry
	}
	return &SliderSolver{
		maxRetry:       maxRetry,
		sliderSelector: ".slider-button, .nc_iconfont.btn_slide, .slide-verify-slider",
		bgImgSelector:  ".slide-verify-bg, canvas.bg",
		gapImgSelector: ".slide-verify-gap, canvas.gap",
	}
}

func (s *SliderSolver) Type() string { return "slider" }

func (s *SliderSolver) Detect(ctx context.Context) (bool, error) {
	return detectSliderCaptcha(ctx)
}

func detectSliderCaptcha(ctx context.Context) (bool, error) {
	if ok, err := detectBaxiaCaptcha(ctx); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	for _, sel := range sliderSelectors {
		ok, err := browser.HasElement(ctx, sel)
		if err == nil && ok {
			return true, nil
		}
	}
	return false, nil
}

func (s *SliderSolver) Solve(ctx context.Context) error {
	if ok, _ := detectBaxiaCaptcha(ctx); ok {
		return solveBaxiaSlideToEnd(ctx, s.maxRetry)
	}
	for attempt := 0; attempt < s.maxRetry; attempt++ {
		bgBytes, err := browser.ScreenshotSelector(ctx, s.bgImgSelector)
		if err != nil || len(bgBytes) == 0 {
			bgBytes = s.captureCanvas(ctx, s.bgImgSelector)
		}
		distance, err := s.calculateGapDistance(bgBytes)
		if err != nil {
			return fmt.Errorf("计算缺口位置失败 (尝试 %d/%d): %w", attempt+1, s.maxRetry, err)
		}
		if err := s.simulateDrag(ctx, s.sliderSelector, distance); err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		detected, _ := s.Detect(ctx)
		if !detected {
			return nil
		}
	}
	return fmt.Errorf("滑块验证失败，已重试%d次", s.maxRetry)
}

func (s *SliderSolver) calculateGapDistance(bgImageBytes []byte) (int, error) {
	if len(bgImageBytes) == 0 {
		return 0, fmt.Errorf("背景图为空")
	}
	img, _, err := image.Decode(bytes.NewReader(bgImageBytes))
	if err != nil {
		return 0, fmt.Errorf("解码背景图失败: %w", err)
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 20 || height <= 4 {
		return 0, fmt.Errorf("背景图尺寸过小: %dx%d", width, height)
	}
	prevDiff := 0.0
	gapLeft := 0
	for x := 10; x < width-10; x++ {
		diff := 0.0
		for y := height / 4; y < height*3/4; y++ {
			r1, g1, b1, _ := img.At(x, y).RGBA()
			r2, g2, b2, _ := img.At(x-1, y).RGBA()
			diff += math.Abs(float64(r1)-float64(r2)) +
				math.Abs(float64(g1)-float64(g2)) +
				math.Abs(float64(b1)-float64(b2))
		}
		diff /= float64(height/2) * 3.0 * 65535.0
		if diff > prevDiff*3 && diff > 0.02 {
			gapLeft = x
			break
		}
		prevDiff = diff
	}
	if gapLeft == 0 {
		gapLeft = width / 3
	}
	return gapLeft, nil
}

func (s *SliderSolver) simulateDrag(ctx context.Context, selector string, distance int) error {
	if distance <= 0 {
		return fmt.Errorf("拖拽距离无效: %d", distance)
	}
	var rect struct {
		BtnX, BtnY, BtnW, BtnH         float64
		TrackX, TrackY, TrackW, TrackH float64
	}
	expr := fmt.Sprintf(`(() => {
		const btn = document.querySelector(%s);
		if (!btn) return null;
		const br = btn.getBoundingClientRect();
		const track = btn.closest('.slider-track, .slide-track, .nc_wrapper, ' +
			'.slider-container, .slide-verify, [class*="slider"], [class*="track"]');
		const tr = track ? track.getBoundingClientRect() : br;
		return {
			btnX: br.x, btnY: br.y, btnW: br.width, btnH: br.height,
			trackX: tr.x, trackY: tr.y, trackW: tr.width, trackH: tr.height
		};
	})()`, strconv.Quote(selector))
	if err := browser.Eval(ctx, expr, &rect); err != nil {
		return fmt.Errorf("获取滑块与轨道位置失败: %w", err)
	}
	if rect.BtnW <= 0 || rect.BtnH <= 0 {
		return fmt.Errorf("滑块元素位置无效: %+v", rect)
	}
	startX := rect.BtnX + rect.BtnW/2
	startY := rect.BtnY + rect.BtnH/2
	trackMidY := rect.TrackY + rect.TrackH/2
	if rect.TrackH <= 0 {
		trackMidY = startY
	}
	if err := browser.MouseMove(ctx, startX, startY); err != nil {
		return err
	}
	if err := browser.Sleep(ctx, 300*time.Millisecond); err != nil {
		return err
	}
	if err := browser.MouseDown(ctx); err != nil {
		return err
	}
	trajectory := generateHumanTrajectory(distance)
	for i, offset := range trajectory[1:] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := browser.MouseMove(ctx, startX+offset, trackMidY+float64(i%3-1)*0.5); err != nil {
			return err
		}
		time.Sleep(dragDelay(i))
	}
	if err := browser.Sleep(ctx, 150*time.Millisecond); err != nil {
		return err
	}
	if err := browser.MouseMove(ctx, startX+float64(distance), trackMidY); err != nil {
		return err
	}
	return browser.MouseUp(ctx)
}

func generateHumanTrajectory(totalDistance int) []float64 {
	if totalDistance <= 0 {
		return []float64{0}
	}
	d := float64(totalDistance)
	steps := 30 + totalDistance/4
	if steps < 25 {
		steps = 25
	}
	if steps > 90 {
		steps = 90
	}
	overshoot := 2.0
	if d < 30 {
		overshoot = 1.0
	}
	peak := d + overshoot
	trajectory := make([]float64, steps)
	trajectory[0] = 0
	sig := func(t float64) float64 { return 1 / (1 + math.Exp(-10*(t-0.5))) }
	norm := sig(1.0)
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps-1)
		trajectory[i] = peak * sig(t) / norm
	}
	retreatSteps := steps/20 + 1
	if retreatSteps > 4 {
		retreatSteps = 4
	}
	for i := 0; i < retreatSteps; i++ {
		idx := steps - 1 - i
		p := float64(i) / float64(retreatSteps-1)
		trajectory[idx] = d + (peak-d)*(1-(1-p)*(1-p))
	}
	trajectory[steps-1] = d
	return trajectory
}

func dragDelay(i int) time.Duration {
	ms := 25 + int(math.Sin(float64(i)*0.4)*15)
	if i < 5 || i > 80 {
		ms += 25
	}
	if i%7 == 4 {
		ms += 35
	}
	if ms < 15 {
		ms = 15
	}
	if ms > 80 {
		ms = 80
	}
	return time.Duration(ms) * time.Millisecond
}

func (s *SliderSolver) captureCanvas(ctx context.Context, selector string) []byte {
	expr := fmt.Sprintf(`(() => {
		const canvas = document.querySelector(%s);
		if (!canvas) return '';
		return canvas.toDataURL('image/png').split(',')[1];
	})()`, strconv.Quote(selector))
	var base64Str string
	if err := browser.Eval(ctx, expr, &base64Str); err != nil || base64Str == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(base64Str)
	if err != nil {
		return nil
	}
	return decoded
}
