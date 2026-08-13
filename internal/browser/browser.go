// Package browser 提供基于 Rod 的无头浏览器引擎与页面操作封装。
//
// 默认无头运行；不加反检测（不注入 stealth、不伪造 UA / AutomationControlled）。
package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

type ctxKey int

const (
	sessionKey ctxKey = iota
)

// session 绑定到 NewContext 返回的 context，惰性创建 *rod.Page。
type session struct {
	mgr  *Manager
	page *rod.Page
	mu   sync.Mutex
}

// Manager 管理一个 Rod 浏览器进程。
type Manager struct {
	cfg     *config.BrowserConfig
	mu      sync.Mutex
	browser *rod.Browser
	cleanup func()
}

// NewManager 初始化浏览器管理器（惰性启动，调用时不拉起 Chrome）。
func NewManager(cfg *config.BrowserConfig) (*Manager, error) {
	if cfg == nil {
		cfg = &config.BrowserConfig{Headless: true, TimeoutSec: 60}
	}
	c := *cfg
	return &Manager{cfg: &c}, nil
}

// ensureBrowser 启动或返回已启动的 Browser。
func (m *Manager) ensureBrowser() (*rod.Browser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.browser != nil {
		return m.browser, nil
	}

	l := launcher.New().Headless(m.cfg.Headless).Leakless(false)
	if m.cfg.ChromePath != "" {
		l = l.Bin(m.cfg.ChromePath)
	} else if path, err := findChrome(); err == nil {
		l = l.Bin(path)
	}
	if m.cfg.DebugPort > 0 {
		l = l.RemoteDebuggingPort(m.cfg.DebugPort)
	}
	// 基础稳定性参数；不加反检测相关 flag
	l = l.Set("no-sandbox").Set("disable-gpu").Set("disable-dev-shm-usage")

	url, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("启动 Chrome 失败: %w", err)
	}
	b := rod.New().ControlURL(url)
	if err := b.Connect(); err != nil {
		l.Cleanup()
		return nil, fmt.Errorf("连接 Chrome 失败: %w", err)
	}
	m.browser = b
	m.cleanup = func() {
		_ = b.Close()
		l.Cleanup()
	}
	return m.browser, nil
}

// NewContext 创建带可选超时的会话上下文；首次 PageFromContext 时打开标签页。
func (m *Manager) NewContext(timeoutSec int) (context.Context, context.CancelFunc) {
	base := context.Background()
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		base, cancel = context.WithTimeout(base, time.Duration(timeoutSec)*time.Second)
	} else {
		base, cancel = context.WithCancel(base)
	}
	s := &session{mgr: m}
	ctx := context.WithValue(base, sessionKey, s)
	return ctx, func() {
		s.mu.Lock()
		p := s.page
		s.page = nil
		s.mu.Unlock()
		if p != nil {
			_ = p.Close()
		}
		cancel()
	}
}

// NewTabContext 与 NewContext 相同（每个会话独立标签页）。
func (m *Manager) NewTabContext(timeoutSec int) (context.Context, context.CancelFunc) {
	return m.NewContext(timeoutSec)
}

// Close 关闭浏览器进程。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cleanup != nil {
		m.cleanup()
		m.cleanup = nil
	}
	m.browser = nil
	return nil
}

// PageFromContext 从 NewContext 会话取出（或惰性创建）页面，并绑定 ctx 超时。
func PageFromContext(ctx context.Context) (*rod.Page, error) {
	s, ok := ctx.Value(sessionKey).(*session)
	if !ok || s == nil {
		return nil, fmt.Errorf("无效的浏览器上下文：请通过 browser.Manager.NewContext 创建")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.page != nil {
		return s.page.Context(ctx), nil
	}
	b, err := s.mgr.ensureBrowser()
	if err != nil {
		return nil, err
	}
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("打开页面失败: %w", err)
	}
	s.page = page
	return page.Context(ctx), nil
}

func findChrome() (string, error) {
	paths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
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
