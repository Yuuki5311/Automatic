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

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

// 编译期断言：SliderSolver 实现 Solver 接口。
var _ Solver = (*SliderSolver)(nil)

// sliderSelectors 常见滑块验证码组件的选择器列表（覆盖交易猫及常见第三方验证码组件）。
var sliderSelectors = []string{
	".slider-button",
	".nc_iconfont.btn_slide",
	".slide-verify-slider",
	"#sliderCaptcha",
	"[class*='slider']",
	"[class*='captcha']",
}

// SliderSolver 基于图像边缘检测的滑块验证码识别器（纯 Go 实现，不依赖 gocv）。
//
// 求解策略：截图带缺口背景图 → 边缘检测计算缺口位置 → 模拟人类拖拽轨迹移动滑块。
type SliderSolver struct {
	maxRetry int // 最大重试次数

	// 滑块/背景图/缺口图选择器（根据交易猫实际页面调整）
	sliderSelector string // 滑块按钮选择器
	bgImgSelector  string // 带缺口的背景图选择器
	gapImgSelector string // 缺口图选择器
}

// NewSliderSolver 创建滑块验证码识别器。
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

// Type 返回验证码类型。
func (s *SliderSolver) Type() string { return "slider" }

// Detect 检测滑块验证码是否出现。
func (s *SliderSolver) Detect(ctx context.Context) (bool, error) {
	return detectSliderCaptcha(ctx)
}

// detectSliderCaptcha 遍历常见滑块选择器，判断页面是否出现滑块验证码。
// 供 SliderSolver 与 ThirdPartySolver 共用。
//
// 注意：CDP executor 只在 chromedp.Run 内部附加到 context，必须经
// chromedp.Run 执行动作，直接调用 .Do(ctx) 会返回 ErrInvalidContext。
func detectSliderCaptcha(ctx context.Context) (bool, error) {
	for _, sel := range sliderSelectors {
		var nodes []*cdp.Node
		err := chromedp.Run(ctx, chromedp.Nodes(sel, &nodes, chromedp.ByQuery, chromedp.AtLeast(0)))
		if err == nil && len(nodes) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// Solve 解决滑块验证码。
// 策略：截图 → 计算缺口位置 → 模拟人类拖拽轨迹 → 移动滑块 → 验证结果（失败则重试）。
func (s *SliderSolver) Solve(ctx context.Context) error {
	for attempt := 0; attempt < s.maxRetry; attempt++ {
		// 1. 截图带缺口的背景图（失败时尝试从 canvas 提取）
		var bgBytes []byte
		if err := chromedp.Run(ctx, chromedp.Screenshot(s.bgImgSelector, &bgBytes, chromedp.ByQuery)); err != nil {
			bgBytes = s.captureCanvas(ctx, s.bgImgSelector)
		}

		// 2. 计算滑块缺口位置
		distance, err := s.calculateGapDistance(bgBytes)
		if err != nil {
			return fmt.Errorf("计算缺口位置失败 (尝试 %d/%d): %w", attempt+1, s.maxRetry, err)
		}

		// 3. 模拟人类拖拽轨迹并移动滑块
		if err := s.simulateDrag(ctx, s.sliderSelector, distance); err != nil {
			// 可能是距离算错了，稍后重试
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}

		// 4. 等待验证码刷新后检查是否已消失
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		detected, _ := s.Detect(ctx)
		if !detected {
			return nil // 验证成功
		}
	}
	return fmt.Errorf("滑块验证失败，已重试%d次", s.maxRetry)
}

// calculateGapDistance 通过图像边缘检测计算缺口左边界距滑块起始位置的像素距离。
//
// 算法（纯 Go）：解码图片后，从左到右扫描列，计算每列与前一列在中间
// 条带区域（高度 1/4~3/4）的平均 RGB 差值；差值出现显著跳变的第一列
// 即为缺口左边界。找不到显著边缘时回退到 1/3 宽度。
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

	// 边缘检测：从左侧扫描，找到第一个显著边缘即为缺口左边界
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
		gapLeft = width / 3 // 默认偏移
	}

	return gapLeft, nil
}

// simulateDrag 模拟人类缓慢拖拽滑块，在指定区域内从左向右滑动。
//
// 流程：定位滑块按钮和轨道区域 → 移动到滑块中心 → 短暂停顿（模拟人类观察）
// → 按下左键 → 沿人类轨迹逐步缓慢移动（每步 20-80ms，总耗时约 3-5 秒，
// 带 Y 轴微抖和不均匀节奏）→ 在终点释放。全程通过 CDP 协议派发鼠标事件，
// 在 headless 模式下执行，不抢占用户物理鼠标。
func (s *SliderSolver) simulateDrag(ctx context.Context, selector string, distance int) error {
	if distance <= 0 {
		return fmt.Errorf("拖拽距离无效: %d", distance)
	}

	// 1. 同时获取滑块按钮和轨道区域的位置
	var rect struct {
		BtnX, BtnY, BtnW, BtnH       float64
		TrackX, TrackY, TrackW, TrackH float64
	}
	expr := fmt.Sprintf(`(() => {
		const btn = document.querySelector(%s);
		if (!btn) return null;
		const br = btn.getBoundingClientRect();
		// 尝试找到滑块所在的轨道容器（常见选择器）
		const track = btn.closest('.slider-track, .slide-track, .nc_wrapper, ' +
			'.slider-container, .slide-verify, [class*="slider"], [class*="track"]');
		const tr = track ? track.getBoundingClientRect() : br;
		return {
			btnX: br.x, btnY: br.y, btnW: br.width, btnH: br.height,
			trackX: tr.x, trackY: tr.y, trackW: tr.width, trackH: tr.height
		};
	})()`, strconv.Quote(selector))
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &rect)); err != nil {
		return fmt.Errorf("获取滑块与轨道位置失败: %w", err)
	}
	if rect.BtnW <= 0 || rect.BtnH <= 0 {
		return fmt.Errorf("滑块元素位置无效: %+v", rect)
	}

	// 起点：滑块按钮中心（即轨道左端）
	startX := rect.BtnX + rect.BtnW/2
	startY := rect.BtnY + rect.BtnH/2
	// 轨道的垂直中心（拖拽过程中 Y 轴在此附近轻微抖动）
	trackMidY := rect.TrackY + rect.TrackH/2
	// 如果没有获取到独立轨道，用滑块 Y 作为 fallback
	if rect.TrackH <= 0 {
		trackMidY = startY
	}

	// 2. 鼠标先移动到滑块中心，短暂停顿模拟人类观察验证码
	if err := chromedp.Run(ctx,
		chromedp.MouseEvent(input.MouseMoved, startX, startY),
	); err != nil {
		return err
	}
	// 人类观察停顿：200-400ms
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(300 * time.Millisecond):
	}

	// 3. 按下鼠标左键
	if err := chromedp.Run(ctx,
		chromedp.MouseEvent(input.MousePressed, startX, startY, chromedp.ButtonLeft),
	); err != nil {
		return err
	}

	// 4. 生成缓慢的人类拖拽轨迹（总耗时约 3-5 秒）
	trajectory := generateHumanTrajectory(distance)

	// 5. 沿轨迹逐步缓慢移动，Y 轴在轨道中心附近轻微抖动
	for i, offset := range trajectory[1:] {
		if err := ctx.Err(); err != nil {
			return err
		}
		curX := startX + offset
		// Y 轴在轨道中心附近 ±1px 内轻微抖动
		curY := trackMidY + float64(i%3-1)*0.5
		if err := chromedp.Run(ctx,
			chromedp.MouseEvent(input.MouseMoved, curX, curY, dragButtons),
		); err != nil {
			return err
		}
		// 缓慢步进：20-80ms 每步，总拖拽约 3-5 秒
		time.Sleep(dragDelay(i))
	}

	// 6. 在终点稍作停顿（模拟人类松手前的确认），然后释放鼠标
	finalX := startX + float64(distance)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}
	if err := chromedp.Run(ctx,
		chromedp.MouseEvent(input.MouseReleased, finalX, trackMidY, chromedp.ButtonLeft),
	); err != nil {
		return err
	}

	return nil
}

// dragButtons 在拖拽移动事件中标记左键处于按下状态（Left=1），
// 使目标页面感知到真实的鼠标拖拽，而非单纯移动。
func dragButtons(p *input.DispatchMouseEventParams) *input.DispatchMouseEventParams {
	return p.WithButtons(1)
}

// generateHumanTrajectory 生成人类拖拽轨迹。
//
// 轨迹特征：先加速 → 匀速 → 减速 → 轻微过冲 → 回退修正到精确落点。
// 函数为确定性纯函数（无随机性），便于测试。
// 总步数控制在 30-90 步，配合 dragDelay 的 20-80ms 步进，总拖拽时长约 3-5 秒。
func generateHumanTrajectory(totalDistance int) []float64 {
	if totalDistance <= 0 {
		return []float64{0}
	}
	d := float64(totalDistance)

	// 每 3-4px 一个采样点，总步数 30-90，保证轨迹平滑但不冗余
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

	// 阶段1：sigmoid 缓动曲线，从 0 平滑升到 peak。
	sig := func(t float64) float64 { return 1 / (1 + math.Exp(-10*(t-0.5))) }
	norm := sig(1.0)
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps-1)
		trajectory[i] = peak * sig(t) / norm
	}

	// 阶段2：末尾 retreatSteps 个采样点从 peak 回退到目标距离 d。
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

// dragDelay 返回第 i 步移动的间隔时长（毫秒），模拟人类缓慢、不匀速的拖拽节奏。
//
// 每步 20-80ms（平均约 40ms），每隔约 7 步有一次额外迟疑（+30-50ms），
// 总拖拽耗时约 3-5 秒，符合真实人类缓慢滑动滑块的习惯。
func dragDelay(i int) time.Duration {
	// 基础步进 20-45ms，随正弦波动模拟手部不均匀移动
	ms := 25 + int(math.Sin(float64(i)*0.4)*15) // 10-40ms
	// 加入缓慢趋势：中段稍快（模拟人类加速段），首尾稍慢
	if i < 5 || i > 80 {
		ms += 25 // 起步和接近终点时更慢（人类犹豫/对准）
	}
	// 偶尔的迟疑停顿
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

// captureCanvas 从 Canvas 元素中提取 PNG 图像数据（截图失败的备用方案）。
func (s *SliderSolver) captureCanvas(ctx context.Context, selector string) []byte {
	expr := fmt.Sprintf(`(() => {
		const canvas = document.querySelector(%s);
		if (!canvas) return '';
		return canvas.toDataURL('image/png').split(',')[1];
	})()`, strconv.Quote(selector))
	// 经 chromedp.Run 执行（CDP executor 只在 Run 内部附加到 context），
	// 否则直接 Evaluate(...).Do(ctx) 会返回 ErrInvalidContext。
	var base64Str string
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &base64Str)); err != nil || base64Str == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(base64Str)
	if err != nil {
		return nil
	}
	return decoded
}
