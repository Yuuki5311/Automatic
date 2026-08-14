package browser

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
)

const (
	defaultMaxConcurrentBrowserSessions = 3
	maxConcurrentBrowserSessions        = 10
)

// Manager 统一管理浏览器端口、Profile 目录与并发名额。
type Manager struct {
	config             Config
	mu                 sync.Mutex
	reserved           map[int]struct{}
	maxBrowserSessions int
	activeBrowserSlots int
	sessionSlotsMu     sync.Mutex
	sessionSlotsCond   sync.Cond
	storeProfilePools  map[string]*storeProfileSlotPool
}

type storeProfileSlotPool struct {
	slots    int
	occupied []bool
	mu       sync.Mutex
}

func NewManager(cfg Config) *Manager {
	maxSessions := cfg.MaxConcurrentSessions
	if maxSessions <= 0 {
		maxSessions = defaultMaxConcurrentBrowserSessions
	}
	if maxSessions > maxConcurrentBrowserSessions {
		maxSessions = maxConcurrentBrowserSessions
	}
	m := &Manager{
		config:             cfg,
		reserved:           make(map[int]struct{}),
		maxBrowserSessions: maxSessions,
		storeProfilePools:  make(map[string]*storeProfileSlotPool),
	}
	m.sessionSlotsCond.L = &m.sessionSlotsMu
	return m
}

// Launch 申请 Profile 槽 + 并发名额后启动浏览器，最多重试 5 次。
func (m *Manager) Launch(ctx context.Context, platform string, storeID, taskID int64, startURL string) (*Session, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	profileDir, releaseSlot, err := m.acquireStoreProfile(acquireCtx, platform, storeID, taskID)
	if err != nil {
		return nil, err
	}
	if err := m.acquireSessionSlot(acquireCtx); err != nil {
		if releaseSlot != nil {
			releaseSlot()
		}
		return nil, err
	}

	launched := false
	defer func() {
		if !launched {
			if releaseSlot != nil {
				releaseSlot()
			}
			m.releaseSessionSlot()
		}
	}()

	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt-1) * 400 * time.Millisecond)
		}
		session, err := m.launchOnceBlocking(platform, storeID, profileDir, startURL, releaseSlot)
		if err == nil {
			launched = true
			session.releaseSessionSlot = func() { m.releaseSessionSlot() }
			return session, nil
		}
		lastErr = err
		if IsBrowserProfileSessionBusyError(err) {
			clearBrowserProfileSingletonLocks(profileDir)
			continue
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("browser launch failed")
	}
	return nil, MapBrowserLaunchError(lastErr, profileDir)
}

func (m *Manager) launchOnceBlocking(platform string, storeID int64, profileDir, startURL string, releaseStoreLock func()) (*Session, error) {
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, err
	}
	if m.config.ClearProfileDir {
		_ = os.RemoveAll(profileDir)
		_ = os.MkdirAll(profileDir, 0o755)
	}
	PrepareProfileForLaunch(profileDir)

	port, releasePort, err := m.reservePort()
	if err != nil {
		return nil, err
	}
	preserveProfile := m.config.UseStoreProfile || !m.config.ClearProfileDir

	targetURL := strings.TrimSpace(startURL)
	if targetURL == "" {
		targetURL = "about:blank"
	}

	app := launcher.NewAppMode(targetURL).
		Leakless(true).
		Headless(m.config.Headless).
		UserDataDir(profileDir).
		RemoteDebuggingPort(port)
	app = applyCommonLauncherSettings(app)

	if m.config.BrowserPath != "" {
		app = app.Bin(m.config.BrowserPath)
	} else if path, ok := launcher.LookPath(); ok {
		app = app.Bin(path)
	}
	if proxy := normalizeBrowserProxyURL(m.config.Proxy); proxy != "" {
		app = app.Proxy(proxy)
	}
	if m.config.FullScreen {
		app = app.Set(flags.Flag("start-fullscreen"))
	}
	if m.config.Kiosk {
		app = app.Set(flags.Flag("kiosk"))
	}
	if !m.config.FullScreen && !m.config.Kiosk {
		w, h := resolveBrowserWindowSize(m.config.WindowSize)
		app = app.Set(flags.Flag("window-size"), fmt.Sprintf("%d,%d", w, h))
	}
	if x, y, ok := parseBrowserWindowPair(m.config.WindowPosition); ok {
		app = app.Set(flags.Flag("window-position"), fmt.Sprintf("%d,%d", x, y))
	}

	controlURL, err := app.Launch()
	if err != nil {
		releasePort()
		if !preserveProfile {
			_ = os.RemoveAll(profileDir)
		}
		return nil, fmt.Errorf("launch browser process: %w", err)
	}

	browserClient := rod.New().NoDefaultDevice().ControlURL(controlURL)
	if err := browserClient.Connect(); err != nil {
		app.Kill()
		releasePort()
		return nil, fmt.Errorf("connect browser: %w", err)
	}

	page, err := pickLaunchPage(browserClient, targetURL)
	if err != nil {
		_ = browserClient.Close()
		app.Kill()
		releasePort()
		return nil, err
	}
	closeExtraLaunchPages(browserClient, page)
	if err := ensureLaunchPageURL(page, targetURL); err != nil {
		_ = page.Close()
		_ = browserClient.Close()
		app.Kill()
		releasePort()
		return nil, err
	}
	if err := waitPageReadyUntil(page, targetURL, time.Now().Add(DefaultPageLoadTimeout)); err != nil {
		_ = page.Close()
		_ = browserClient.Close()
		app.Kill()
		releasePort()
		return nil, fmt.Errorf("wait browser page load: %w", err)
	}

	return &Session{
		Platform:                platform,
		StoreID:                 storeID,
		ControlURL:              controlURL,
		ProfileDir:              profileDir,
		DebugPort:               port,
		launcher:                app,
		browser:                 browserClient,
		page:                    page,
		releasePort:             releasePort,
		preserveProfileDir:      preserveProfile,
		releaseStoreProfileLock: releaseStoreLock,
	}, nil
}

func applyCommonLauncherSettings(app *launcher.Launcher) *launcher.Launcher {
	app = app.Preferences(commonBrowserPreferencesJSON)
	app.Set(flags.Flag("lang"), "zh-CN")
	app.Append(flags.Flag("disable-features"), "PasswordManagerOnboarding")
	app.Set(flags.Flag("disable-session-crashed-bubble"), "")
	app.Set(flags.Flag("hide-crash-restore-bubble"), "")
	app.Set(flags.Flag("no-first-run"), "")
	app.Set(flags.Flag("no-default-browser-check"), "")
	app.Set(flags.Flag("disk-cache-size"), "33554432")
	app.Set(flags.Flag("media-cache-size"), "16777216")
	return app
}

func normalizeBrowserProxyURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}

func resolveBrowserWindowSize(value string) (int, int) {
	if w, h, ok := parseBrowserWindowPair(value); ok {
		return w, h
	}
	return defaultBrowserWindowWidth, defaultBrowserWindowHeight
}

func parseBrowserWindowPair(value string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	first, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	second, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || first <= 0 || second <= 0 {
		return 0, 0, false
	}
	return first, second, true
}

func (m *Manager) reservePort() (int, func(), error) {
	for i := 0; i < 64; i++ {
		port := browserDebugPortMin + rand.Intn(browserDebugPortMax-browserDebugPortMin+1)
		if port == 9229 {
			continue
		}
		m.mu.Lock()
		if _, used := m.reserved[port]; used {
			m.mu.Unlock()
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			m.mu.Unlock()
			continue
		}
		_ = ln.Close()
		m.reserved[port] = struct{}{}
		m.mu.Unlock()
		release := func() {
			m.mu.Lock()
			delete(m.reserved, port)
			m.mu.Unlock()
		}
		return port, release, nil
	}
	return 0, nil, fmt.Errorf("no available debug port")
}

func (m *Manager) acquireSessionSlot(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.sessionSlotsMu.Lock()
		if m.activeBrowserSlots < m.maxBrowserSessions {
			m.activeBrowserSlots++
			m.sessionSlotsMu.Unlock()
			return nil
		}
		m.sessionSlotsMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) releaseSessionSlot() {
	m.sessionSlotsMu.Lock()
	if m.activeBrowserSlots > 0 {
		m.activeBrowserSlots--
	}
	m.sessionSlotsCond.Signal()
	m.sessionSlotsMu.Unlock()
}

func (m *Manager) acquireStoreProfile(ctx context.Context, platform string, storeID, taskID int64) (string, func(), error) {
	if !m.config.UseStoreProfile {
		return TaskProfileDir(m.config.ProfileRoot, platform, storeID, taskID), nil, nil
	}
	slots := m.config.StoreProfileSlots
	if slots <= 0 {
		slots = 1
	}
	key := platform + ":" + strconv.FormatInt(storeID, 10)
	pool := m.getOrCreatePool(key, slots)
	return pool.acquire(ctx, func(slot int) string {
		return StoreProfileDir(m.config.ProfileRoot, platform, storeID, slot, slots)
	})
}

func (m *Manager) getOrCreatePool(key string, slots int) *storeProfileSlotPool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pool, ok := m.storeProfilePools[key]
	if !ok || pool == nil || pool.slots != slots {
		pool = &storeProfileSlotPool{slots: slots, occupied: make([]bool, slots)}
		m.storeProfilePools[key] = pool
	}
	return pool
}

func (p *storeProfileSlotPool) acquire(ctx context.Context, dirFn func(slot int) string) (string, func(), error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		p.mu.Lock()
		for i := 0; i < p.slots; i++ {
			if !p.occupied[i] {
				p.occupied[i] = true
				slot := i
				p.mu.Unlock()
				return dirFn(slot), func() {
					p.mu.Lock()
					p.occupied[slot] = false
					p.mu.Unlock()
				}, nil
			}
		}
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// RemoveStoreProfileDirs 删除指定店铺在本机的 profile 目录树。
func (m *Manager) RemoveStoreProfileDirs(platform string, storeID int64) error {
	root := strings.TrimSpace(m.config.ProfileRoot)
	if root == "" {
		return nil
	}
	storeDir := filepath.Join(root, platform, strconv.FormatInt(storeID, 10))
	if err := os.RemoveAll(storeDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除浏览器 Profile 目录失败: %w", err)
	}
	return nil
}
