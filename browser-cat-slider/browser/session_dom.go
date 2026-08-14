// Package browser 封装 go-rod 浏览器启动、Profile 管理与页面操作。
//
// 主项目对应目录：internal/browser/
package browser

import (
	"fmt"
	"strconv"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// OpenWithTimeout 打开指定 URL 并在给定超时内等待页面就绪。
func (s *Session) OpenWithTimeout(url string, timeout time.Duration) error {
	page, err := s.MainPage()
	if err != nil {
		return err
	}
	return navigateAndWaitReady(page, url, timeout)
}

// IsJSTrue 判断 JavaScript 执行结果是否等于期望值（通常为 "true"）。
func (s *Session) IsJSTrue(js, yesStr string, args ...interface{}) bool {
	_, data := s.JavaScript(js, args...)
	return data == yesStr
}

// Input 通过 CSS 选择器填写输入框，并触发 input/change 事件以兼容 React/Vue 表单。
func (s *Session) Input(selector string, text string, timeout time.Duration) error {
	_ = timeout // 参考实现用 JS 直接操作，不阻塞等待元素
	err, _ := s.JavaScript(fmt.Sprintf(`
		const selector = %s;
		const value = %s;
		const el = document.querySelector(selector);
		if (!el) {
			return "missing";
		}
		el.scrollIntoView?.({ behavior: "smooth", block: "center" });
		el.focus?.();
		const descriptor = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")
			|| Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value");
		if (descriptor && descriptor.set) {
			descriptor.set.call(el, value);
		} else {
			el.value = value;
		}
		el.dispatchEvent(new Event("input", { bubbles: true }));
		el.dispatchEvent(new Event("change", { bubbles: true }));
		return String(el.value || "") === value ? "ok" : "mismatch";
	`, strconv.Quote(selector), strconv.Quote(text)))
	if err != nil {
		return err
	}
	return nil
}

// WaitElementVisible 轮询等待 CSS 选择器对应元素可见。
func (s *Session) WaitElementVisible(selector string, timeout time.Duration) (*rod.Element, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.IsJSTrue(fmt.Sprintf(`
			const el = document.querySelector(%s);
			if (!el) return false;
			const rect = el.getBoundingClientRect();
			const style = window.getComputedStyle(el);
			return !!(rect.width && rect.height && style.display !== "none" && style.visibility !== "hidden" && style.opacity !== "0");
		`, strconv.Quote(selector)), "true") {
			page, err := s.MainPage()
			if err != nil {
				return nil, err
			}
			el, err := page.Element(selector)
			return el, err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("wait element visible timeout: %s", selector)
}

// HTML 返回当前页面完整 HTML，用于检测登录表单关键字。
func (s *Session) HTML() (string, error) {
	page, err := s.timedPage()
	if err != nil {
		return "", err
	}
	return page.HTML()
}

// Cookies 读取当前页面 Cookie 列表；可传入 URL 限定域名。
func (s *Session) Cookies(urls ...string) ([]*proto.NetworkCookie, error) {
	page, err := s.MainPage()
	if err != nil {
		return nil, err
	}
	return page.Cookies(urls)
}

// SetCookies 向浏览器注入 Cookie（通常在工作台域名 .jiaoyimao.com 下）。
func (s *Session) SetCookies(cookies []*proto.NetworkCookieParam) error {
	page, err := s.MainPage()
	if err != nil {
		return err
	}
	return page.SetCookies(cookies)
}

// ClearCookies 清空当前浏览器会话中的全部 Cookie。
func (s *Session) ClearCookies() error {
	page, err := s.MainPage()
	if err != nil {
		return err
	}
	return page.SetCookies(nil)
}
