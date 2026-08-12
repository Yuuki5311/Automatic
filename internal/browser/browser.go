// Package browser 提供基于 chromedp 的无头浏览器引擎与页面操作封装。
//
// 所有浏览器操作均在 headless 模式下执行：不显示窗口、不抢占鼠标，
// 适合服务器端定时抓取场景。管理器负责浏览器的创建、上下文（标签页）
// 管理与关闭；动作封装提供导航、点击、输入、提取、截图、Cookie 注入
// 等可组合的 chromedp.Action。
package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

// Manager 管理一个无头浏览器进程的生命周期与页面上下文。
type Manager struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	opts        []chromedp.ExecAllocatorOption
}

// NewManager 初始化浏览器管理器。
//
// headless 模式不显示窗口，所有操作在后端执行，不抢占鼠标。
// 创建管理器并不会立即启动浏览器进程，浏览器会在首次执行动作时惰性启动；
// 因此即使本机未安装 Chrome，调用本函数也不会报错。
func NewManager(cfg *config.BrowserConfig) (*Manager, error) {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", cfg.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		// 设置窗口大小，确保元素可被定位和点击
		chromedp.WindowSize(1920, 1080),
		// 禁用自动化检测
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"),
	}

	if cfg.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(cfg.ChromePath))
	} else {
		// 自动查找Chrome路径
		if path, err := findChrome(); err == nil {
			opts = append(opts, chromedp.ExecPath(path))
		}
	}

	if cfg.DebugPort > 0 {
		opts = append(opts, chromedp.Flag("remote-debugging-port", fmt.Sprintf("%d", cfg.DebugPort)))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	return &Manager{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		opts:        opts,
	}, nil
}

// NewContext 创建带超时的浏览器上下文。
//
// timeoutSec > 0 时，上下文自带截止时间；首次在该上下文上执行动作时
// 会惰性启动浏览器进程。timeoutSec 建议不小于浏览器冷启动时间（数秒）。
func (m *Manager) NewContext(timeoutSec int) (context.Context, context.CancelFunc) {
	ctx, cancel := chromedp.NewContext(m.allocCtx)
	if timeoutSec > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	}
	return ctx, cancel
}

// NewTabContext 创建新的浏览器标签页上下文，与既有标签页相互独立，
// 可并行执行不同页面的操作。取消该上下文只会关闭对应标签页。
func (m *Manager) NewTabContext(timeoutSec int) (context.Context, context.CancelFunc) {
	return m.NewContext(timeoutSec)
}

// Close 关闭浏览器并释放临时资源。
func (m *Manager) Close() error {
	m.allocCancel()
	return nil
}

// findChrome 在Windows上自动查找Chrome安装路径。
// 优先检查系统级安装目录，再检查用户级安装目录（%LOCALAPPDATA%）。
func findChrome() (string, error) {
	paths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	// 用户级安装路径（不依赖 C:\Users\Default 模板目录）
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths, filepath.Join(local, `Google\Chrome\Application\chrome.exe`))
	}
	paths = append(paths, `C:\Users\Default\AppData\Local\Google\Chrome\Application\chrome.exe`)

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到Chrome，请在配置文件中指定chrome_path")
}
