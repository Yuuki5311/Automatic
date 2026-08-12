package captcha

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

// --- generateHumanTrajectory ---

func TestGenerateHumanTrajectory(t *testing.T) {
	cases := []int{5, 50, 100, 200, 500}
	for _, d := range cases {
		tr := generateHumanTrajectory(d)

		// 采样点数量符合约定
		if want := 50 + d/5; len(tr) != want {
			t.Errorf("distance=%d: 轨迹采样点数量 = %d, want %d", d, len(tr), want)
		}
		// 起点为 0（起始位置）
		if tr[0] != 0 {
			t.Errorf("distance=%d: 轨迹起点 = %v, want 0", d, tr[0])
		}
		// 终点精确落在目标距离（落点修正）
		if tr[len(tr)-1] != float64(d) {
			t.Errorf("distance=%d: 轨迹终点 = %v, want %d", d, tr[len(tr)-1], d)
		}
		// 所有点非负且不超过过冲峰值
		overshoot := 2.0
		if d < 30 {
			overshoot = 1.0
		}
		peak := float64(d) + overshoot
		for i, v := range tr {
			if v < 0 || v > peak {
				t.Errorf("distance=%d: 轨迹点[%d] = %v 超出范围 [0, %v]", d, i, v, peak)
			}
		}
	}
}

func TestGenerateHumanTrajectoryDragProfile(t *testing.T) {
	// 轨迹特征：加速 → 匀速 → 减速 → 过冲 → 回退修正
	d := 200
	tr := generateHumanTrajectory(d)

	// 1. 出现轻微过冲（最大值超过目标距离）
	peakIdx, maxVal := -1, -1.0
	for i, v := range tr {
		if v > maxVal {
			maxVal, peakIdx = v, i
		}
	}
	if maxVal <= float64(d) {
		t.Errorf("轨迹未出现过冲: max=%v, 目标=%d", maxVal, d)
	}
	if peakIdx >= len(tr)-1 {
		t.Errorf("过冲点不应是最后一点（终点需回退修正落点）: peakIdx=%d, len=%d", peakIdx, len(tr))
	}

	// 2. 过冲点之前单调递增（加速→减速）
	for i := 1; i <= peakIdx; i++ {
		if tr[i] < tr[i-1] {
			t.Fatalf("过冲点前应单调递增: [%d]=%v < [%d]=%v", i, tr[i], i-1, tr[i-1])
		}
	}
	// 3. 过冲点之后单调递减（回退修正）
	for i := peakIdx + 1; i < len(tr); i++ {
		if tr[i] > tr[i-1] {
			t.Fatalf("过冲点后应单调递减（回退）: [%d]=%v > [%d]=%v", i, tr[i], i-1, tr[i-1])
		}
	}

	// 4. 轨迹速度特征：初始段平均步长小（加速起步），末端步长小（减速/修正）
	third := len(tr) / 3
	avgStart, avgEnd := 0.0, 0.0
	for i := 1; i <= third; i++ {
		avgStart += tr[i] - tr[i-1]
	}
	for i := len(tr) - third; i < len(tr); i++ {
		avgEnd += tr[i] - tr[i-1]
	}
	avgStart /= float64(third)
	avgEnd /= float64(third)
	if avgStart > avgEnd*3 {
		t.Errorf("末段步长应显著小于起步段（减速特征）: avgStart=%.2f, avgEnd=%.2f", avgStart, avgEnd)
	}
}

func TestGenerateHumanTrajectoryDeterministic(t *testing.T) {
	// 纯函数：相同输入必须产生相同轨迹
	a := generateHumanTrajectory(150)
	b := generateHumanTrajectory(150)
	if !reflect.DeepEqual(a, b) {
		t.Error("轨迹生成应为确定性纯函数（无随机性）")
	}
}

func TestGenerateHumanTrajectoryInvalidDistance(t *testing.T) {
	for _, d := range []int{0, -10} {
		tr := generateHumanTrajectory(d)
		if len(tr) != 1 || tr[0] != 0 {
			t.Errorf("distance=%d: 应返回单点零轨迹, got %v", d, tr)
		}
	}
}

// --- dragDelay ---

func TestDragDelay(t *testing.T) {
	for i := 0; i < 500; i++ {
		dd := dragDelay(i)
		if dd <= 0 || dd > 20*time.Millisecond {
			t.Errorf("dragDelay(%d) = %v 超出合理范围 (0, 20ms]", i, dd)
		}
	}
	if dragDelay(3) != dragDelay(3) {
		t.Error("dragDelay 应为确定性函数")
	}
}

// --- calculateGapDistance ---

// makeCaptchaBG 生成一张合成验证码背景图：
// 白底 + 从 gapX 起宽 gapWidth 的黑色竖条（模拟缺口）。
func makeCaptchaBG(width, height, gapX, gapWidth int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.RGBA{R: 20, G: 20, B: 20, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x >= gapX && x < gapX+gapWidth {
				img.Set(x, y, black)
			} else {
				img.Set(x, y, white)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestCalculateGapDistanceFindsGap(t *testing.T) {
	s := NewSliderSolver(&config.CaptchaConfig{MaxRetry: 3})
	const (
		width    = 300
		height   = 120
		gapX     = 150
		gapWidth = 10
	)
	got, err := s.calculateGapDistance(makeCaptchaBG(width, height, gapX, gapWidth))
	if err != nil {
		t.Fatalf("calculateGapDistance 返回错误: %v", err)
	}
	// 缺口左边界应落在缺口起始位置附近（±2px 容差）
	if got < gapX-2 || got > gapX+2 {
		t.Errorf("缺口位置 = %d, 期望在 %d±2 附近", got, gapX)
	}
}

func TestCalculateGapDistanceFallback(t *testing.T) {
	// 无边缘的纯色图片应回退到默认偏移（宽度的 1/3）
	s := NewSliderSolver(nil)
	got, err := s.calculateGapDistance(makeCaptchaBG(300, 120, -1, 0))
	if err != nil {
		t.Fatalf("calculateGapDistance 返回错误: %v", err)
	}
	if want := 300 / 3; got != want {
		t.Errorf("无边缘时回退距离 = %d, want %d", got, want)
	}
}

func TestCalculateGapDistanceErrors(t *testing.T) {
	s := NewSliderSolver(nil)
	if _, err := s.calculateGapDistance(nil); err == nil {
		t.Error("空背景图应返回错误")
	}
	if _, err := s.calculateGapDistance([]byte("not-an-image")); err == nil {
		t.Error("非法图片数据应返回错误")
	}
	if _, err := s.calculateGapDistance([]byte("\x89PNG\r\n\x1a\n")); err == nil {
		t.Error("截断的 PNG 数据应返回错误")
	}
}

// --- 构造函数 ---

func TestNewSliderSolverDefaults(t *testing.T) {
	s := NewSliderSolver(nil)
	if s.maxRetry != 3 {
		t.Errorf("默认 maxRetry = %d, want 3", s.maxRetry)
	}
	if s.Type() != "slider" {
		t.Errorf("Type() = %q, want \"slider\"", s.Type())
	}
	if s.sliderSelector == "" || s.bgImgSelector == "" || s.gapImgSelector == "" {
		t.Error("默认选择器不应为空")
	}
}

func TestNewSliderSolverWithConfig(t *testing.T) {
	s := NewSliderSolver(&config.CaptchaConfig{MaxRetry: 5})
	if s.maxRetry != 5 {
		t.Errorf("maxRetry = %d, want 5", s.maxRetry)
	}
}
