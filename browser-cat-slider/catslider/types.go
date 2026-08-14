package catslider

import (
	"time"

	"reference/browser-cat-slider/browser"
)

// Session 是滑块模块依赖的浏览器会话能力（对应主项目 *browser.Session）。
type Session = browser.Session

// SliderRect 表示滑块区域 bounding box。
type SliderRect struct {
	X, Y, Width, Height int
}

const (
	RetryCount              = 3
	RefreshCycleCount       = 3
	RefreshWaitMinSec       = 5
	RefreshWaitMaxSec       = 10
	PunishRestartWaitMinSec = 120
	PunishRestartWaitMaxSec = 180
	LockPollInterval        = 200 * time.Millisecond
	LockMaxWait             = 10 * time.Minute
	PhaseTimeout            = 5 * time.Minute
	PunishPhaseTimeout      = 15 * time.Minute
	RodTimeout              = 45 * time.Second
	TraceMaxDuration        = 90 * time.Second
	MinRemainingBeforeDrag  = TraceMaxDuration
	PollInterval            = 500 * time.Millisecond
)

// Mode 滑块展示形态。
const (
	ModeDialog     = "dialog"
	ModeInline     = "inline"
	ModePunishPage = "punish_page"
)

// ChallengeOptions 滑块挑战处理选项。
type ChallengeOptions struct {
	Phase        string
	AfterRefresh func() error
	AfterRestart func() error
}

// Probe 滑块弹窗探测结果。
type Probe struct {
	FailureText string  `json:"failureText"`
	Escalated   bool    `json:"escalated"`
	HasHandle   bool    `json:"hasHandle"`
	StartX      float64 `json:"startX"`
	StartY      float64 `json:"startY"`
	Distance    float64 `json:"distance"`
	Mode        string  `json:"mode"`
}

// DragMetrics 一次滑块拖动使用的坐标参数。
type DragMetrics struct {
	StartX, StartY, Distance float64
	Mode                     string
}
