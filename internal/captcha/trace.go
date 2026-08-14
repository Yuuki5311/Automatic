package captcha

// trace.go：参考 browser-cat-slider 的人工滑块轨迹加载、缩放与回放。
// 轨迹目录优先：CAT_SLIDER_TRACE_DIR → browser-cat-slider/runtime/data/cat-slider-traces

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/example/jiaoyimao-scraper/internal/browser"
)

var (
	traceLinePattern = regexp.MustCompile(`^(\d+)-(\d+)-(\d+)-(\d+)-鼠标-(.*)$`)
	traceOnce        sync.Once
	tracePool        []sliderTrace
	traceLoadErr     error
	tracePickerMu    sync.Mutex
	traceRemaining   []int
	tracePickerSize  int
)

const traceMaxDuration = 90 * time.Second

type tracePoint struct {
	Index, X, Y, DelayMs int
	Down, Up             bool
}

type sliderTrace struct {
	Source             string
	Points             []tracePoint
	downIndex, upIndex int
}

type replayStep struct {
	X, Y     float64
	Delay    time.Duration
	Down, Up bool
}

type dragPlan struct {
	SourceTrace string
	TargetDist  float64
	Steps       []replayStep
}

func traceSearchDirs() []string {
	dirs := []string{}
	if custom := strings.TrimSpace(os.Getenv("CAT_SLIDER_TRACE_DIR")); custom != "" {
		dirs = append(dirs, custom)
	}
	dirs = append(dirs,
		filepath.Join("browser-cat-slider", "runtime", "data", "cat-slider-traces"),
		filepath.Join("runtime", "data", "cat-slider-traces"),
	)
	if root := findModuleRoot(); root != "" {
		dirs = append(dirs, filepath.Join(root, "browser-cat-slider", "runtime", "data", "cat-slider-traces"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "browser-cat-slider", "runtime", "data", "cat-slider-traces"))
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

func findModuleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir = wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func getSliderTraces() ([]sliderTrace, error) {
	traceOnce.Do(func() {
		tracePool, traceLoadErr = loadTracesFromDirs(traceSearchDirs())
		if traceLoadErr == nil {
			slog.Info("已加载滑块人工轨迹", "component", "captcha", "count", len(tracePool))
		} else {
			slog.Warn("未加载滑块人工轨迹，将使用算法回退", "component", "captcha", "error", traceLoadErr)
		}
	})
	return tracePool, traceLoadErr
}

func loadTracesFromDirs(dirs []string) ([]sliderTrace, error) {
	var tried []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			tried = append(tried, dir)
			continue
		}
		names := []string{}
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
				names = append(names, entry.Name())
			}
		}
		if len(names) == 0 {
			tried = append(tried, dir)
			continue
		}
		sort.Strings(names)
		traces := make([]sliderTrace, 0, len(names))
		for _, name := range names {
			trace, err := parseTraceFile(filepath.Join(dir, name))
			if err == nil {
				traces = append(traces, trace)
			}
		}
		if len(traces) > 0 {
			return traces, nil
		}
		tried = append(tried, dir)
	}
	return nil, fmt.Errorf("未找到交易猫滑块轨迹文件（已试: %s），可设置 CAT_SLIDER_TRACE_DIR", strings.Join(tried, "; "))
}

func parseTraceFile(path string) (sliderTrace, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return sliderTrace{}, err
	}
	text, err := decodeTraceText(content)
	if err != nil {
		return sliderTrace{}, err
	}
	trace := sliderTrace{Source: filepath.Base(path)}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		matches := traceLinePattern.FindStringSubmatch(line)
		if len(matches) != 6 {
			continue
		}
		index, _ := strconv.Atoi(matches[1])
		x, _ := strconv.Atoi(matches[2])
		y, _ := strconv.Atoi(matches[3])
		delayMs, _ := strconv.Atoi(matches[4])
		eventText := strings.TrimSpace(matches[5])
		trace.Points = append(trace.Points, tracePoint{
			Index: index, X: x, Y: y, DelayMs: delayMs,
			Down: strings.Contains(eventText, "左键按下"),
			Up:   strings.Contains(eventText, "左键弹起"),
		})
	}
	if err := trace.prepare(); err != nil {
		return sliderTrace{}, err
	}
	return trace, nil
}

func decodeTraceText(content []byte) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("轨迹文件为空")
	}
	if strings.Contains(string(content), "鼠标") {
		return string(content), nil
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(content)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func (t *sliderTrace) prepare() error {
	t.downIndex, t.upIndex = -1, -1
	for i, p := range t.Points {
		if p.Down && t.downIndex < 0 {
			t.downIndex = i
		}
		if p.Up {
			t.upIndex = i
		}
	}
	if t.downIndex < 0 || t.upIndex <= t.downIndex {
		return fmt.Errorf("轨迹缺少完整的按下/弹起事件")
	}
	if t.Points[t.upIndex].X <= t.Points[t.downIndex].X {
		return fmt.Errorf("轨迹横向位移无效")
	}
	return nil
}

func (t sliderTrace) buildDragPlan(startX, startY, distance float64, rng *rand.Rand) (dragPlan, error) {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	downPoint := t.Points[t.downIndex]
	upPoint := t.Points[t.upIndex]
	recordedDx := float64(upPoint.X - downPoint.X)
	if recordedDx <= 0 {
		return dragPlan{}, fmt.Errorf("轨迹横向距离无效")
	}
	targetDistance := distance + float64(rng.Intn(9))
	scaleX := targetDistance / recordedDx
	plan := dragPlan{SourceTrace: t.Source, TargetDist: targetDistance}
	appendStep := func(x, y float64, delayMs int, down, up bool) {
		factor := 0.85 + rng.Float64()*0.30
		delay := time.Duration(float64(delayMs)*factor) * time.Millisecond
		plan.Steps = append(plan.Steps, replayStep{X: x, Y: y, Delay: delay, Down: down, Up: up})
	}
	for i := 0; i < t.downIndex; i++ {
		p := t.Points[i]
		appendStep(startX+float64(p.X-downPoint.X), startY+float64(p.Y-downPoint.Y), p.DelayMs, false, false)
	}
	appendStep(startX, startY, downPoint.DelayMs, true, false)
	for i := t.downIndex + 1; i < t.upIndex; i++ {
		p := t.Points[i]
		appendStep(startX+float64(p.X-downPoint.X)*scaleX, startY+float64(p.Y-downPoint.Y), p.DelayMs, false, false)
	}
	appendStep(startX+float64(upPoint.X-downPoint.X)*scaleX, startY+float64(upPoint.Y-downPoint.Y), upPoint.DelayMs, false, true)
	if len(plan.Steps) == 0 {
		return dragPlan{}, fmt.Errorf("轨迹回放步骤为空")
	}
	return plan, nil
}

func pickTrace(traces []sliderTrace, rng *rand.Rand) (sliderTrace, error) {
	if len(traces) == 0 {
		return sliderTrace{}, fmt.Errorf("没有可用的交易猫滑块轨迹")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	tracePickerMu.Lock()
	defer tracePickerMu.Unlock()
	if tracePickerSize != len(traces) {
		traceRemaining = nil
		tracePickerSize = len(traces)
	}
	if len(traceRemaining) == 0 {
		traceRemaining = make([]int, len(traces))
		for i := range traces {
			traceRemaining[i] = i
		}
		rng.Shuffle(len(traceRemaining), func(i, j int) {
			traceRemaining[i], traceRemaining[j] = traceRemaining[j], traceRemaining[i]
		})
	}
	last := len(traceRemaining) - 1
	picked := traceRemaining[last]
	traceRemaining = traceRemaining[:last]
	return traces[picked], nil
}

func replayDragPlan(ctx context.Context, plan dragPlan) error {
	if len(plan.Steps) == 0 {
		return fmt.Errorf("滑块轨迹回放参数无效")
	}
	dragStart := time.Now()
	for _, step := range plan.Steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Since(dragStart) > traceMaxDuration {
			return fmt.Errorf("滑块轨迹回放超过 %s", traceMaxDuration)
		}
		if step.Delay > 0 {
			if err := browser.Sleep(ctx, step.Delay); err != nil {
				return err
			}
		}
		if step.Down {
			if err := browser.MouseMove(ctx, step.X, step.Y); err != nil {
				return err
			}
			if err := browser.MouseDown(ctx); err != nil {
				return err
			}
			continue
		}
		if err := browser.MouseMove(ctx, step.X, step.Y); err != nil {
			if step.Up {
				_ = browser.MouseUp(ctx)
			}
			return err
		}
		if step.Up {
			if err := browser.MouseUp(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// dragWithHumanTrace 优先回放人工轨迹；失败则 cubic ease-out + 过冲回退（对齐 browser-cat-slider）。
func dragWithHumanTrace(ctx context.Context, startX, startY float64, distance int) error {
	if distance <= 0 {
		return fmt.Errorf("拖拽距离无效: %d", distance)
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	cooldown := time.Duration(1000+rng.Intn(2001)) * time.Millisecond
	slog.Info("滑块拖动前冷却", "component", "captcha", "wait_ms", cooldown.Milliseconds())
	if err := browser.Sleep(ctx, cooldown); err != nil {
		return err
	}

	traces, traceErr := getSliderTraces()
	if traceErr == nil && len(traces) > 0 {
		trace, err := pickTrace(traces, rng)
		if err == nil {
			plan, err := trace.buildDragPlan(startX, startY, float64(distance), rng)
			if err == nil {
				slog.Info("使用人工轨迹拖动滑块", "component", "captcha",
					"source", plan.SourceTrace, "steps", len(plan.Steps),
					"distance", distance, "target", plan.TargetDist)
				if err := replayDragPlan(ctx, plan); err != nil {
					return err
				}
				settle := time.Duration(1000+rng.Intn(2001)) * time.Millisecond
				return browser.Sleep(ctx, settle)
			}
			slog.Warn("轨迹缩放失败，回退算法", "component", "captcha", "source", trace.Source, "error", err)
		}
	} else if traceErr != nil {
		slog.Warn("轨迹不可用，回退算法", "component", "captcha", "error", traceErr)
	}
	return dragBaxiaEasing(ctx, startX, startY, distance, rng)
}

func dragBaxiaEasing(ctx context.Context, startX, startY float64, distance int, rng *rand.Rand) error {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	steps := 18 + rng.Intn(10)
	if err := browser.MouseMove(ctx, startX, startY); err != nil {
		return err
	}
	// 参考实现：先点一下再等待，模拟犹豫
	if err := browser.MouseDown(ctx); err != nil {
		return err
	}
	if err := browser.MouseUp(ctx); err != nil {
		return err
	}
	if err := browser.Sleep(ctx, time.Duration(500+rng.Intn(1500))*time.Millisecond); err != nil {
		return err
	}
	if err := browser.MouseMove(ctx, startX, startY); err != nil {
		return err
	}
	if err := browser.MouseDown(ctx); err != nil {
		return err
	}
	overshoot := rng.Intn(100) < 35
	targetX := startX + float64(distance)
	if overshoot {
		targetX += float64(rng.Intn(14) + 5)
	}
	targetY := startY + float64(rng.Intn(7)-3)
	for step := 1; step <= steps; step++ {
		if err := ctx.Err(); err != nil {
			_ = browser.MouseUp(ctx)
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
		if err := browser.MouseMove(ctx, x, y); err != nil {
			_ = browser.MouseUp(ctx)
			return err
		}
		if err := browser.Sleep(ctx, time.Duration(10+rng.Intn(25))*time.Millisecond); err != nil {
			_ = browser.MouseUp(ctx)
			return err
		}
	}
	if err := browser.MouseMove(ctx, startX+float64(distance), startY); err != nil {
		_ = browser.MouseUp(ctx)
		return err
	}
	if err := browser.Sleep(ctx, time.Duration(80+rng.Intn(120))*time.Millisecond); err != nil {
		_ = browser.MouseUp(ctx)
		return err
	}
	if err := browser.MouseUp(ctx); err != nil {
		return err
	}
	return browser.Sleep(ctx, time.Duration(1000+rng.Intn(1500))*time.Millisecond)
}
