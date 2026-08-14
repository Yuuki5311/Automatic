package browser

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	commonBrowserPreferencesJSON = `{"credentials_enable_service":false,"profile":{"password_manager_enabled":false,"password_manager_leak_detection":false,"exit_type":"Normal"},"session":{"restore_on_startup":5},"intl":{"accept_languages":"zh-CN,zh"}}`
	browserDebugPortMin          = 20000
	browserDebugPortMax          = 48000
	defaultBrowserWindowWidth    = 1166
	defaultBrowserWindowHeight   = 1012
	browserJavaScriptTimeout     = 45 * time.Second
	browserOperationRetryCount   = 2
	browserOperationRetryDelay   = 400 * time.Millisecond
)

// Session 表示一次浏览器会话，封装 rod.Browser 与 rod.Page。
type Session struct {
	Platform    string
	StoreID     int64
	ControlURL  string
	ProfileDir  string
	DebugPort   int

	launcher              *launcher.Launcher
	browser               *rod.Browser
	page                  *rod.Page
	actionMu              sync.RWMutex
	actionLogger          func(string)
	closeOnce             sync.Once
	closedIntentionally   atomic.Bool
	releasePort             func()
	releaseSessionSlot      func()
	preserveProfileDir      bool
	releaseStoreProfileLock func()
}

func (s *Session) MainPage() (*rod.Page, error) {
	if s.page != nil {
		return s.page, nil
	}
	if s.browser == nil {
		return nil, fmt.Errorf("browser is not connected")
	}
	page, err := s.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, err
	}
	s.page = page
	return page, nil
}

func (s *Session) timedPage() (*rod.Page, error) {
	page, err := s.MainPage()
	if err != nil {
		return nil, err
	}
	return page.Timeout(browserJavaScriptTimeout), nil
}

func (s *Session) SetActionLogger(logger func(string)) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.actionLogger = logger
}

func (s *Session) LogAction(format string, args ...any) {
	s.actionMu.RLock()
	logger := s.actionLogger
	s.actionMu.RUnlock()
	if logger != nil {
		logger(fmt.Sprintf(format, args...))
	}
}

func (s *Session) Open(url string) error {
	page, err := s.MainPage()
	if err != nil {
		return err
	}
	return navigateAndWaitReady(page, url, DefaultPageLoadTimeout)
}

func (s *Session) Refresh() error {
	page, err := s.MainPage()
	if err != nil {
		return err
	}
	return refreshAndWaitReady(page, DefaultPageLoadTimeout)
}

func (s *Session) WaitStable(timeout time.Duration) error {
	page, err := s.MainPage()
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	maxWait := timeout + 5*time.Second
	if maxWait < 8*time.Second {
		maxWait = 8 * time.Second
	}
	err = page.Timeout(maxWait).WaitStable(timeout)
	if err == nil || IsNavigationRaceError(err) {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return nil
	}
	return err
}

func (s *Session) CurrentURL() (string, error) {
	page, err := s.timedPage()
	if err != nil {
		return "", err
	}
	info, err := page.Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

// JavaScript 在页面执行 JS 并返回字符串结果（与主项目 Session.JavaScript 行为一致）。
func (s *Session) JavaScript(js string, args ...interface{}) (error, string) {
	var lastErr error
	for attempt := 1; attempt <= browserOperationRetryCount+1; attempt++ {
		if attempt > 1 {
			time.Sleep(browserOperationRetryDelay)
		}
		err, result := s.javaScriptOnce(js, args...)
		if err == nil {
			return nil, result
		}
		lastErr = err
		if !IsTransientOperationError(err) || attempt > browserOperationRetryCount {
			return err, ""
		}
	}
	return lastErr, ""
}

func (s *Session) javaScriptOnce(js string, args ...interface{}) (error, string) {
	page, err := s.timedPage()
	if err != nil {
		return err, ""
	}
	wrapped := "return String((function () {\n" + js + "\n})())"
	remote, err := page.Eval(normalizeEvalScript(wrapped), args...)
	if err != nil {
		return fmt.Errorf("JS执行失败：%w", err), ""
	}
	if remote == nil {
		return nil, ""
	}
	return nil, remote.Value.String()
}

func normalizeEvalScript(js string) string {
	script := strings.TrimSpace(js)
	if script == "" {
		return "() => String(\"\")"
	}
	if strings.HasPrefix(script, "() =>") || strings.HasPrefix(script, "async () =>") {
		return script
	}
	return "() => String((function () {\n" + script + "\n})())"
}

func (s *Session) ClosedIntentionally() bool {
	return s.closedIntentionally.Load()
}

// Close 关闭浏览器；保留 profile 时做 slim 缓存清理。
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.closedIntentionally.Store(true)
		if s.launcher != nil {
			s.launcher.Kill()
			s.launcher.Cleanup()
		}
		if s.page != nil {
			_ = s.page.Close()
		}
		if s.browser != nil {
			_ = s.browser.Close()
		}
		if s.releaseStoreProfileLock != nil {
			s.releaseStoreProfileLock()
		}
		if s.preserveProfileDir && strings.TrimSpace(s.ProfileDir) != "" {
			clearBrowserProfileSingletonLocks(s.ProfileDir)
			_, _ = SlimProfileDir(s.ProfileDir)
		} else if !s.preserveProfileDir && strings.TrimSpace(s.ProfileDir) != "" {
			clearBrowserProfileSingletonLocks(s.ProfileDir)
			_ = os.RemoveAll(s.ProfileDir)
		}
		if s.releasePort != nil {
			s.releasePort()
		}
		if s.releaseSessionSlot != nil {
			s.releaseSessionSlot()
		}
	})
}
