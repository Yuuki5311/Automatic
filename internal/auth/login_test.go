package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

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

func TestCookieEntriesToData_PreservesFieldsAndMaxExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	entries := []models.CookieEntry{
		{
			Name: "jym_token", Value: "abc123", Domain: ".jiaoyimao.com", Path: "/",
			Expires: 1900000000, HTTPOnly: true, Secure: true,
		},
		{
			Name: "sessionid", Value: "s1", Domain: "merchant.jiaoyimao.com", Path: "/",
			Expires: 1800000000,
		},
	}
	data := cookieEntriesToData(entries, now)
	if len(data.Cookies) != 2 {
		t.Fatalf("converted %d cookies, want 2", len(data.Cookies))
	}
	first := data.Cookies[0]
	if first.Name != "jym_token" || first.Value != "abc123" || !first.HTTPOnly || !first.Secure {
		t.Fatalf("first cookie mismatch: %+v", first)
	}
	wantExpiry := time.Unix(1900000000, 0)
	if !data.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", data.ExpiresAt, wantExpiry)
	}
}

func TestCookieEntriesToData_SessionCookiesFallbackTo24h(t *testing.T) {
	now := time.Now()
	entries := []models.CookieEntry{
		{Name: "token", Value: "v", Domain: "127.0.0.1", Path: "/", Expires: -1},
		{Name: "other", Value: "o", Domain: "127.0.0.1", Path: "/", Expires: 0},
	}
	data := cookieEntriesToData(entries, now)
	want := now.Add(24 * time.Hour)
	if !data.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want default %v", data.ExpiresAt, want)
	}
}

func TestCookieEntriesToData_EmptyList(t *testing.T) {
	now := time.Now()
	data := cookieEntriesToData(nil, now)
	if data == nil || len(data.Cookies) != 0 {
		t.Fatalf("unexpected: %+v", data)
	}
	if !data.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want default 24h", data.ExpiresAt)
	}
}

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
	got, err := (&LoginService{}).RefreshIfNeeded(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("RefreshIfNeeded with valid cookie failed: %v", err)
	}
	if got == nil || len(got.Cookies) != 1 || got.Cookies[0].Value != "secret" {
		t.Fatalf("RefreshIfNeeded returned %+v, want existing cookie data", got)
	}
}

func TestRefreshIfNeeded_InvalidCookieAttemptsLogin(t *testing.T) {
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
	if !strings.Contains(err.Error(), "无效的浏览器上下文") && !strings.Contains(err.Error(), "加载登录页失败") {
		t.Fatalf("error = %v, want browser context / navigate failure", err)
	}
}

func TestPollForCaptcha_NoCaptcha(t *testing.T) {
	solver := &stubSolver{detectResult: false}
	err := pollForCaptcha(context.Background(), solver, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("pollForCaptcha with no captcha returned error: %v", err)
	}
}

func TestPollForCaptcha_DetectAndSolve(t *testing.T) {
	solver := &stubSolver{detectResult: true}
	err := pollForCaptcha(context.Background(), solver, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("pollForCaptcha solve failed: %v", err)
	}
}

func TestPollForCaptcha_SolveError(t *testing.T) {
	solver := &stubSolver{detectResult: true, solveErr: context.Canceled}
	err := pollForCaptcha(context.Background(), solver, 3, time.Millisecond)
	if err == nil {
		t.Fatal("expected solve error")
	}
}
