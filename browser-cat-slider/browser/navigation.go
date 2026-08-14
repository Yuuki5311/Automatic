package browser

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const (
	pageReadyPollInterval = 200 * time.Millisecond
	pageReadyOpTimeout    = 3 * time.Second
	// DefaultPageLoadTimeout 页面打开/刷新/等待加载的默认超时。
	DefaultPageLoadTimeout = 30 * time.Second
)

// navigateAndWaitReady 导航并等待 document.readyState 为 complete/interactive。
func navigateAndWaitReady(page *rod.Page, url string, timeout time.Duration) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	if timeout <= 0 {
		timeout = DefaultPageLoadTimeout
	}
	trimmedURL := strings.TrimSpace(url)
	deadline := time.Now().Add(timeout)
	navigateTimeout := timeout
	if navigateTimeout > pageReadyOpTimeout {
		navigateTimeout = pageReadyOpTimeout
	}
	if err := page.Timeout(navigateTimeout).Navigate(trimmedURL); err != nil && !IsNavigationRaceError(err) {
		return err
	}
	return waitPageReadyUntil(page, trimmedURL, deadline)
}

// refreshAndWaitReady 刷新并等待页面就绪。
func refreshAndWaitReady(page *rod.Page, timeout time.Duration) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	if timeout <= 0 {
		timeout = DefaultPageLoadTimeout
	}
	deadline := time.Now().Add(timeout)
	reloadTimeout := timeout
	if reloadTimeout > pageReadyOpTimeout {
		reloadTimeout = pageReadyOpTimeout
	}
	if err := page.Timeout(reloadTimeout).Reload(); err != nil && !IsNavigationRaceError(err) {
		return err
	}
	return waitPageReadyUntil(page, "", deadline)
}

func waitPageReadyUntil(page *rod.Page, expectedURL string, deadline time.Time) error {
	var lastState string
	var lastErr error
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		opTimeout := pageReadyOpTimeout
		if remaining < opTimeout {
			opTimeout = remaining
		}
		if opTimeout <= 0 {
			break
		}
		state, err := pageReadyState(page, opTimeout)
		if err == nil {
			lastState = state
			if state == "complete" || state == "interactive" {
				trimmed := strings.TrimSpace(expectedURL)
				if trimmed == "" || IsBlankPageURL(trimmed) {
					return nil
				}
				currentURL, urlErr := pageCurrentURL(page, opTimeout)
				if urlErr == nil && urlsMatch(trimmed, currentURL) {
					return nil
				}
			}
		} else {
			lastErr = err
		}
		sleepFor := pageReadyPollInterval
		if remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("page ready timeout (last readyState=%q): %w", lastState, lastErr)
	}
	return fmt.Errorf("page ready timeout (last readyState=%q)", lastState)
}

func pageReadyState(page *rod.Page, opTimeout time.Duration) (string, error) {
	timedPage := page
	if opTimeout > 0 {
		timedPage = page.Timeout(opTimeout)
	}
	result, err := proto.RuntimeEvaluate{
		Expression:    "document.readyState",
		ReturnByValue: true,
	}.Call(timedPage)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("read document.readyState returned nil")
	}
	return strings.Trim(strings.TrimSpace(result.Result.Value.Str()), `"`), nil
}

func pageCurrentURL(page *rod.Page, opTimeout time.Duration) (string, error) {
	timedPage := page
	if opTimeout > 0 {
		timedPage = page.Timeout(opTimeout)
	}
	info, err := timedPage.Info()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(info.URL), nil
}

func urlsMatch(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	if expected == actual {
		return true
	}
	return strings.TrimRight(expected, "/") == strings.TrimRight(actual, "/")
}

// IsBlankPageURL 判断是否为空白页。
func IsBlankPageURL(rawURL string) bool {
	switch strings.ToLower(strings.TrimSpace(rawURL)) {
	case "", "about:blank", "about:srcdoc", "chrome://newtab/", "chrome://new-tab-page/":
		return true
	default:
		return false
	}
}
