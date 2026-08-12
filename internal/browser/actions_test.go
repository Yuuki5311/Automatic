package browser

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// TestActionWrappersReturnActions 验证所有动作封装返回非 nil 的 chromedp.Action。
func TestActionWrappersReturnActions(t *testing.T) {
	var text string
	var buf []byte

	cases := []struct {
		name string
		act  chromedp.Action
	}{
		{"Navigate", Navigate("https://example.com")},
		{"WaitVisible", WaitVisible("#app")},
		{"WaitNotVisible", WaitNotVisible("#loading")},
		{"WaitReady", WaitReady()},
		{"Click", Click("#btn")},
		{"Input", Input("#input", "text")},
		{"ExtractHTML", ExtractHTML("#app", &text)},
		{"ExtractText", ExtractText("#app", &text)},
		{"Screenshot", Screenshot("shot.png")},
		{"ScreenshotBytes", ScreenshotBytes("#captcha", &buf)},
		{"Sleep", Sleep(10 * time.Millisecond)},
		{"SetCookies", SetCookies(nil)},
		{"ScrollIntoView", ScrollIntoView("#target")},
		{"WaitForNetworkIdle", WaitForNetworkIdle(time.Second)},
	}
	for _, tc := range cases {
		if tc.act == nil {
			t.Errorf("%s returned nil action", tc.name)
		}
	}
}

func TestBuildCookieParams(t *testing.T) {
	expiresUnix := float64(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC).Unix())
	entry := models.CookieEntry{
		Name:     "jym_token",
		Value:    "secret-value",
		Domain:   ".jiaoyimao.com",
		Path:     "/",
		Expires:  expiresUnix,
		HTTPOnly: true,
		Secure:   true,
	}

	p := buildCookieParams(entry)
	if p == nil {
		t.Fatal("buildCookieParams returned nil")
	}
	if p.Name != "jym_token" {
		t.Errorf("Name = %q, want jym_token", p.Name)
	}
	if p.Value != "secret-value" {
		t.Errorf("Value = %q, want secret-value", p.Value)
	}
	if p.Domain != ".jiaoyimao.com" {
		t.Errorf("Domain = %q, want .jiaoyimao.com", p.Domain)
	}
	if p.Path != "/" {
		t.Errorf("Path = %q, want /", p.Path)
	}
	if !p.HTTPOnly {
		t.Error("HTTPOnly should be true")
	}
	if !p.Secure {
		t.Error("Secure should be true")
	}
	if p.Expires == nil {
		t.Fatal("Expires should not be nil when entry.Expires > 0")
	}
	if got, want := p.Expires.Time().Unix(), int64(expiresUnix); got != want {
		t.Errorf("Expires = %d, want %d", got, want)
	}
}

func TestBuildCookieParamsSessionCookie(t *testing.T) {
	// Expires == 0 表示会话级 Cookie，不应设置过期时间
	entry := models.CookieEntry{Name: "sid", Value: "v", Domain: "example.com", Path: "/"}
	p := buildCookieParams(entry)
	if p.Expires != nil {
		t.Errorf("Expires should be nil for session cookie, got %v", p.Expires)
	}
	if p.Secure || p.HTTPOnly {
		t.Errorf("defaults should stay false, got Secure=%v HTTPOnly=%v", p.Secure, p.HTTPOnly)
	}
}

// TestSetCookiesWithBadContext 验证 SetCookies 在无浏览器上下文中返回错误而不是 panic。
func TestSetCookiesWithBadContext(t *testing.T) {
	act := SetCookies([]models.CookieEntry{{Name: "a", Value: "b"}})
	err := act.Do(badTestContext())
	if err == nil {
		t.Fatal("expected an error when running SetCookies without a browser context")
	}
	if !errors.Is(err, cdp.ErrInvalidContext) {
		t.Errorf("expected cdp.ErrInvalidContext, got: %v", err)
	}
}

// badTestContext 返回一个未经过 chromedp.NewContext 包装的普通上下文，
// 用于验证动作封装在无效上下文上安全报错。
func badTestContext() context.Context {
	return context.Background()
}
