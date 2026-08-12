package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// stubSolver 测试用验证码识别器：可按需注入 Detect / Solve 的返回结果。
type stubSolver struct {
	detectResult bool
	detectErr    error
	solveErr     error
}

var _ captcha.Solver = (*stubSolver)(nil)

func (s *stubSolver) Detect(ctx context.Context) (bool, error) {
	return s.detectResult, s.detectErr
}

func (s *stubSolver) Solve(ctx context.Context) error {
	return s.solveErr
}

func (s *stubSolver) Type() string { return "stub" }

// ---------- cookiesToData ----------

func TestCookiesToData_PreservesFieldsAndMaxExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cdpCookies := []*network.Cookie{
		{
			Name:     "jym_token",
			Value:    "abc123",
			Domain:   ".jiaoyimao.com",
			Path:     "/",
			Expires:  1900000000,
			HTTPOnly: true,
			Secure:   true,
		},
		{
			Name:    "sessionid",
			Value:   "s1",
			Domain:  "merchant.jiaoyimao.com",
			Path:    "/",
			Expires: 1800000000,
		},
	}

	data := cookiesToData(cdpCookies, now)

	if len(data.Cookies) != 2 {
		t.Fatalf("converted %d cookies, want 2", len(data.Cookies))
	}
	first := data.Cookies[0]
	if first.Name != "jym_token" || first.Value != "abc123" ||
		first.Domain != ".jiaoyimao.com" || first.Path != "/" ||
		first.Expires != 1900000000 || !first.HTTPOnly || !first.Secure {
		t.Fatalf("first cookie mismatch: %+v", first)
	}
	second := data.Cookies[1]
	if second.HTTPOnly || second.Secure {
		t.Fatalf("second cookie flags mismatch: %+v", second)
	}

	// ExpiresAt 取所有Cookie中最晚的过期时间
	wantExpiry := time.Unix(1900000000, 0)
	if !data.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", data.ExpiresAt, wantExpiry)
	}
	if !data.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", data.UpdatedAt, now)
	}
}

func TestCookiesToData_SessionCookiesFallbackTo24h(t *testing.T) {
	now := time.Now()
	// 会话级Cookie的CDP Expires为-1；全部为会话Cookie时应回退到默认24小时
	cdpCookies := []*network.Cookie{
		{Name: "token", Value: "v", Domain: "127.0.0.1", Path: "/", Expires: -1},
		{Name: "other", Value: "o", Domain: "127.0.0.1", Path: "/", Expires: 0},
	}

	data := cookiesToData(cdpCookies, now)

	want := now.Add(24 * time.Hour)
	if !data.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want default %v", data.ExpiresAt, want)
	}
	if len(data.Cookies) != 2 {
		t.Fatalf("converted %d cookies, want 2", len(data.Cookies))
	}
}

func TestCookiesToData_SkipsNilEntries(t *testing.T) {
	cdpCookies := []*network.Cookie{
		nil,
		{Name: "token", Value: "v", Domain: "127.0.0.1", Path: "/", Expires: -1},
	}
	data := cookiesToData(cdpCookies, time.Now())
	if len(data.Cookies) != 1 || data.Cookies[0].Name != "token" {
		t.Fatalf("nil cookie entry not skipped, got %+v", data.Cookies)
	}
}

func TestCookiesToData_EmptyList(t *testing.T) {
	now := time.Now()
	data := cookiesToData(nil, now)
	if data == nil {
		t.Fatal("cookiesToData(nil) returned nil, want empty CookieData")
	}
	if len(data.Cookies) != 0 {
		t.Fatalf("converted %d cookies from empty list, want 0", len(data.Cookies))
	}
	if !data.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want default 24h", data.ExpiresAt)
	}
}

// ---------- RefreshIfNeeded ----------

func TestRefreshIfNeeded_ValidCookieSkipsLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	want := &models.CookieData{
		Cookies:   []models.CookieEntry{{Name: "jym_token", Value: "secret"}},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := SaveCookies(path, want); err != nil {
		t.Fatalf("SaveCookies failed: %v", err)
	}

	cfg := &config.Config{JYM: config.JYMConfig{CookiePath: path}}
	// Cookie 有效时无需浏览器，也不应触发登录
	got, err := (&LoginService{}).RefreshIfNeeded(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("RefreshIfNeeded with valid cookie failed: %v", err)
	}
	if got == nil || len(got.Cookies) != 1 || got.Cookies[0].Value != "secret" {
		t.Fatalf("RefreshIfNeeded returned %+v, want existing cookie data", got)
	}
}

func TestRefreshIfNeeded_InvalidCookieAttemptsLogin(t *testing.T) {
	// Cookie 文件不存在 → 触发 PerformLogin；测试上下文未关联浏览器，
	// chromedp.Run 应立即返回 ErrInvalidContext，无需真实Chrome。
	cfg := &config.Config{JYM: config.JYMConfig{
		BaseURL:    "https://example.com",
		Username:   "u",
		Password:   "p",
		CookiePath: filepath.Join(t.TempDir(), "missing.json"),
	}}
	_, err := (&LoginService{}).RefreshIfNeeded(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("RefreshIfNeeded with missing cookie returned nil error, want login attempt to fail")
	}
	if !errors.Is(err, chromedp.ErrInvalidContext) {
		t.Fatalf("error = %v, want to wrap chromedp.ErrInvalidContext", err)
	}
}

// ---------- pollForCaptcha ----------

func TestPollForCaptcha_NoCaptcha(t *testing.T) {
	solver := &stubSolver{detectResult: false}
	err := pollForCaptcha(context.Background(), solver, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("pollForCaptcha with no captcha returned error: %v", err)
	}
}

func TestPollForCaptcha_DetectErrorContinuesPolling(t *testing.T) {
	solver := &stubSolver{detectResult: false, detectErr: errors.New("detect boom")}
	err := pollForCaptcha(context.Background(), solver, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("pollForCaptcha should tolerate Detect errors and continue: %v", err)
	}
}

func TestPollForCaptcha_SolveError(t *testing.T) {
	solver := &stubSolver{detectResult: true, solveErr: errors.New("solve boom")}
	err := pollForCaptcha(context.Background(), solver, 3, time.Millisecond)
	if err == nil {
		t.Fatal("pollForCaptcha with failing Solve returned nil error")
	}
	if !errors.Is(err, solver.solveErr) {
		t.Fatalf("error = %v, want wrapped solve error", err)
	}
}

func TestPollForCaptcha_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	solver := &stubSolver{detectResult: false}
	err := pollForCaptcha(ctx, solver, 20, time.Second)
	if err == nil {
		t.Fatal("pollForCaptcha with canceled context returned nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
