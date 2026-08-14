package catslider

// trajectory.go：人工滑块轨迹文件的加载、缩放与 Rod 鼠标回放。
//
// 轨迹格式：{index}-{x}-{y}-{delayMs}-鼠标-{左键按下|左键弹起|----}

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"golang.org/x/text/encoding/simplifiedchinese"
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

type tracePoint struct {
	Index, X, Y, DelayMs int
	Down, Up             bool
}

type sliderTrace struct {
	Source              string
	Points              []tracePoint
	downIndex, upIndex  int
}

type replayStep struct {
	X, Y  float64
	Delay time.Duration
	Down, Up bool
}

type dragPlan struct {
	SourceTrace string
	TargetDist  float64
	Steps       []replayStep
}

type dragReplayHooks struct {
	OnDown    func(replayStep)
	OnStep    func(index, total int, step replayStep)
	OnRelease func(replayStep)
}

func traceSearchDirs() []string {
	// 默认目录：相对当前工作目录；参考模块已内置 runtime/data/cat-slider-traces（84 条人工轨迹）。
	dirs := []string{
		filepath.Join("runtime", "data", "cat-slider-traces"),
		filepath.Join("reference", "browser-cat-slider", "runtime", "data", "cat-slider-traces"),
	}
	if custom := strings.TrimSpace(os.Getenv("CAT_SLIDER_TRACE_DIR")); custom != "" {
		dirs = append([]string{custom}, dirs...)
	}
	seen := map[string]struct{}{}
	unique := []string{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		unique = append(unique, dir)
	}
	return unique
}

func getSliderTraces() ([]sliderTrace, error) {
	traceOnce.Do(func() {
		tracePool, traceLoadErr = loadTracesFromDirs(traceSearchDirs())
	})
	return tracePool, traceLoadErr
}

func loadTracesFromDirs(dirs []string) ([]sliderTrace, error) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := []string{}
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
				names = append(names, entry.Name())
			}
		}
		if len(names) == 0 {
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
	}
	return nil, fmt.Errorf("未找到交易猫滑块轨迹文件，请检查 runtime/data/cat-slider-traces 或设置 CAT_SLIDER_TRACE_DIR")
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

func timedPage(page *rod.Page) *rod.Page {
	if page == nil {
		return page
	}
	return page.Timeout(RodTimeout)
}

func replayDragPlan(ctx context.Context, page *rod.Page, plan dragPlan, maxDuration time.Duration, hooks dragReplayHooks) error {
	if page == nil || len(plan.Steps) == 0 {
		return fmt.Errorf("滑块轨迹回放参数无效")
	}
	if maxDuration <= 0 {
		maxDuration = TraceMaxDuration
	}
	mousePage := timedPage(page)
	dragStart := time.Now()
	for index, step := range plan.Steps {
		if err := failIfContextDone(ctx); err != nil {
			return err
		}
		if time.Since(dragStart) > maxDuration {
			return fmt.Errorf("滑块轨迹回放超过 %s", maxDuration)
		}
		if step.Delay > 0 {
			if err := sleepContext(ctx, step.Delay); err != nil {
				return err
			}
		}
		point := proto.NewPoint(step.X, step.Y)
		if step.Down {
			if err := mousePage.Mouse.MoveTo(point); err != nil {
				return err
			}
			if err := mousePage.Mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
				return err
			}
			if hooks.OnDown != nil {
				hooks.OnDown(step)
			}
			continue
		}
		if err := mousePage.Mouse.MoveTo(point); err != nil {
			if step.Up {
				_ = mousePage.Mouse.Up(proto.InputMouseButtonLeft, 1)
			}
			return err
		}
		if hooks.OnStep != nil && (index <= 1 || index >= len(plan.Steps)-2 || index%8 == 0) {
			hooks.OnStep(index+1, len(plan.Steps), step)
		}
		if step.Up {
			if err := mousePage.Mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
				return err
			}
			if hooks.OnRelease != nil {
				hooks.OnRelease(step)
			}
		}
	}
	return nil
}

// 轨迹文件每行格式示例：
// 1-367-692-1078-鼠标-左键按下---
// 2-367-692-109-鼠标----
// 26-766-726-0-鼠标-左键弹起---
