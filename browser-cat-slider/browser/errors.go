package browser

import (
	"context"
	"errors"
	"strings"
)

// IsTransientOperationError 判断浏览器操作是否属于可重试的瞬时异常。
func IsTransientOperationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	transientHints := []string{
		"context canceled",
		"context deadline exceeded",
		"use of closed network connection",
		"inspected target navigated or closed",
		"connection reset",
		"broken pipe",
		"eof",
		"target closed",
		"page load error net::err_aborted",
	}
	for _, hint := range transientHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

// IsBrowserCrashError 判断错误是否表示浏览器进程或 CDP 会话已失效。
func IsBrowserCrashError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	crashHints := []string{
		"use of closed network connection",
		"inspected target navigated or closed",
		"connection reset",
		"broken pipe",
		"target closed",
		"websocket",
		"no such target",
		"browser connection",
		"session closed",
		"not connected",
		"chrome not reachable",
		"protocol error",
	}
	for _, hint := range crashHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	if message == "eof" || strings.HasSuffix(message, ": eof") {
		return true
	}
	return false
}

// IsNavigationRaceError 判断是否为页面跳转导致的 rod 目标切换错误（可继续轮询）。
func IsNavigationRaceError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "inspected target navigated or closed") ||
		strings.Contains(message, "target navigated or closed") ||
		strings.Contains(message, "target closed")
}

// IsBrowserProfileSessionBusyError 判断是否 Profile 被现有浏览器占用。
func IsBrowserProfileSessionBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	lower := strings.ToLower(message)
	return strings.Contains(lower, "failed to get the debug url") ||
		strings.Contains(lower, "opening in existing browser session") ||
		strings.Contains(message, "正在现有的浏览器会话中打开") ||
		strings.Contains(message, "现有的浏览器会话")
}
