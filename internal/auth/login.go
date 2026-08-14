package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

const (
	captchaPollTries    = 60
	captchaPollInterval = 500 * time.Millisecond
	captchaSolveWait    = 3 * time.Second
	postLoginWaitTries  = 40
)

type LoginService struct{}

func (s *LoginService) PerformLogin(
	ctx context.Context,
	cfg *config.Config,
	captchaSolver captcha.Solver,
) (*models.CookieData, error) {
	loginURL := cfg.JYM.LoginURL
	if loginURL == "" {
		loginURL = cfg.JYM.BaseURL + "/workbench"
	}
	slog.Info("导航到登录页", "component", "login", "url", loginURL)

	if err := browser.Navigate(ctx, loginURL); err != nil {
		return nil, fmt.Errorf("加载登录页失败: %w", err)
	}
	_ = browser.Sleep(ctx, 8*time.Second)

	for i := 0; i < 20; i++ {
		var bodyText string
		_ = browser.Eval(ctx, `document.body ? document.body.innerText.substring(0,200) : ""`, &bodyText)
		if len(bodyText) > 20 {
			break
		}
		if err := browser.Sleep(ctx, 3*time.Second); err != nil {
			return nil, err
		}
	}

	_ = browser.Eval(ctx, `(()=>{
		for(const el of document.querySelectorAll('*')){
			if(el.childNodes.length===1 && el.textContent.trim()==='密码登录'){el.click();return}
		}
	})()`, nil)
	_ = browser.Sleep(ctx, time.Second)

	_ = browser.Eval(ctx, `(()=>{
		for(const el of document.querySelectorAll('input[type="checkbox"]')){
			if(!el.checked){el.click()}
		}
	})()`, nil)

	phoneSel := `input[type="text"], input[type="tel"]`
	passSel := `input[type="password"]`
	slog.Info("开始输入账号密码", "component", "login")

	if err := browser.Click(ctx, phoneSel); err != nil {
		return nil, fmt.Errorf("聚焦手机号失败: %w", err)
	}
	_ = browser.Sleep(ctx, 200*time.Millisecond)
	if err := browser.Input(ctx, phoneSel, cfg.JYM.Username); err != nil {
		return nil, fmt.Errorf("输入账号失败: %w", err)
	}
	_ = browser.Sleep(ctx, 300*time.Millisecond)
	if err := browser.Click(ctx, passSel); err != nil {
		return nil, fmt.Errorf("聚焦密码失败: %w", err)
	}
	_ = browser.Sleep(ctx, 200*time.Millisecond)
	if err := browser.Input(ctx, passSel, cfg.JYM.Password); err != nil {
		return nil, fmt.Errorf("输入密码失败: %w", err)
	}
	_ = browser.Sleep(ctx, 500*time.Millisecond)
	slog.Info("账号密码已输入", "component", "login")

	var clicked bool
	_ = browser.Eval(ctx, `(()=>{
		for(const el of document.querySelectorAll('button')){
			if(el.textContent.includes('立即登录')){el.click();return true}
		}
		return false
	})()`, &clicked)
	slog.Info("点击立即登录", "component", "login", "clicked", clicked)

	_ = browser.Sleep(ctx, 2*time.Second)
	_ = browser.Eval(ctx, `(()=>{
		for(const el of document.querySelectorAll('button, span, div')){
			if(el.textContent.trim()==='同意'){el.click();return}
		}
	})()`, nil)
	slog.Info("已点同意", "component", "login")
	_ = browser.Sleep(ctx, 2*time.Second)

	currentURL, _ := browser.Location(ctx)
	slog.Info("当前URL", "component", "login", "url", currentURL)

	if err := s.waitAndSolveCaptcha(ctx, captchaSolver); err != nil {
		return nil, err
	}

	for i := 0; i < postLoginWaitTries; i++ {
		currentURL, _ = browser.Location(ctx)
		if !strings.Contains(currentURL, "login") {
			break
		}
		if ok, _ := captchaSolver.Detect(ctx); ok {
			slog.Info("验证码再次出现，继续处理", "component", "login")
			if err := captchaSolver.Solve(ctx); err != nil {
				return nil, fmt.Errorf("二次验证码失败: %w", err)
			}
		}
		if err := browser.Sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
	}

	if strings.Contains(currentURL, "login") {
		slog.Info("仍在登录域，尝试进入工作台", "component", "login", "url", currentURL)
		_ = browser.Navigate(ctx, cfg.JYM.BaseURL+"/workbench")
		_ = browser.WaitReady(ctx)
		_ = browser.Sleep(ctx, 3*time.Second)
		currentURL, _ = browser.Location(ctx)
		if strings.Contains(currentURL, "login") {
			return nil, fmt.Errorf("登录失败，停留在登录页（滑块可能未通过）: %s", currentURL)
		}
	}

	cookieData, err := s.extractCookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("提取Cookie失败: %w", err)
	}
	if !IsCookieValid(cookieData) {
		return nil, fmt.Errorf("登录后未拿到有效会话 Cookie（共 %d 个），请检查滑块是否通过", len(cookieData.Cookies))
	}
	slog.Info("登录完成", "component", "login", "cookies", len(cookieData.Cookies))
	return cookieData, nil
}

func (s *LoginService) RefreshIfNeeded(ctx context.Context, cfg *config.Config, captchaSolver captcha.Solver) (*models.CookieData, error) {
	existing, err := LoadCookies(cfg.JYM.CookiePath)
	if err == nil && IsCookieValid(existing) {
		slog.Info("Cookie有效，跳过登录", "component", "cookie")
		return existing, nil
	}
	slog.Warn("Cookie无效或过期，执行自动登录", "component", "cookie")
	if strings.TrimSpace(cfg.JYM.Password) == "" {
		return nil, fmt.Errorf("Cookie 无效且无密码可自动登录")
	}
	newCookies, err := s.PerformLogin(ctx, cfg, captchaSolver)
	if err != nil {
		return nil, err
	}
	if err := SaveCookies(cfg.JYM.CookiePath, newCookies); err != nil {
		slog.Error("保存Cookie失败", "component", "cookie", "error", err)
	}
	return newCookies, nil
}

// ProbeStillOnLoginPage 将 Cookie 注入浏览器并打开工作台，若仍在登录页返回 true。
func (s *LoginService) ProbeStillOnLoginPage(ctx context.Context, cfg *config.Config, cookies *models.CookieData) (bool, error) {
	if cfg == nil || cookies == nil {
		return false, fmt.Errorf("参数无效")
	}
	base := cfg.JYM.BaseURL
	if base == "" {
		base = "https://merchant.jiaoyimao.com"
	}
	if err := browser.Navigate(ctx, base+"/workbench"); err != nil {
		return false, err
	}
	_ = browser.SetCookies(ctx, cookies.Cookies)
	if err := browser.Navigate(ctx, base+"/workbench"); err != nil {
		return false, err
	}
	_ = browser.Sleep(ctx, 3*time.Second)
	_ = browser.WaitReady(ctx)
	u, _ := browser.Location(ctx)
	return strings.Contains(u, "login"), nil
}

func (s *LoginService) waitAndSolveCaptcha(ctx context.Context, solver captcha.Solver) error {
	return pollForCaptcha(ctx, solver, captchaPollTries, captchaPollInterval)
}

func pollForCaptcha(ctx context.Context, solver captcha.Solver, tries int, interval time.Duration) error {
	for i := 0; i < tries; i++ {
		currentURL, _ := browser.Location(ctx)
		if currentURL != "" && !strings.Contains(currentURL, "login") {
			slog.Info("已离开登录页，跳过验证码等待", "component", "login", "url", currentURL)
			return nil
		}
		detected, err := solver.Detect(ctx)
		if err != nil {
			slog.Warn("验证码检测出错", "component", "login", "error", err)
			continue
		}
		if detected {
			slog.Info("检测到验证码", "component", "login", "type", solver.Type())
			if err := solver.Solve(ctx); err != nil {
				return fmt.Errorf("验证码解决失败: %w", err)
			}
			slog.Info("验证码已解决", "component", "login")
			_ = browser.Sleep(ctx, captchaSolveWait)
			return nil
		}
		if err := browser.Sleep(ctx, interval); err != nil {
			return err
		}
	}
	slog.Info("未检测到验证码", "component", "login")
	return nil
}

func (s *LoginService) extractCookies(ctx context.Context) (*models.CookieData, error) {
	currentURL, _ := browser.Location(ctx)
	urls := []string{
		currentURL,
		"https://member.jiaoyimao.com/",
		"https://merchant.jiaoyimao.com/",
		"https://www.jiaoyimao.com/",
		"https://mtop.jiaoyimao.com/",
	}
	entries, err := browser.GetCookies(ctx, urls)
	if err != nil {
		return nil, err
	}
	return cookieEntriesToData(entries, time.Now()), nil
}

func cookieEntriesToData(entries []models.CookieEntry, now time.Time) *models.CookieData {
	var maxExpires float64
	for _, c := range entries {
		if c.Expires > maxExpires {
			maxExpires = c.Expires
		}
	}
	expiresAt := now.Add(24 * time.Hour)
	if maxExpires > 0 {
		expiresAt = time.Unix(int64(maxExpires), 0)
	}
	out := make([]models.CookieEntry, len(entries))
	copy(out, entries)
	return &models.CookieData{Cookies: out, UpdatedAt: now, ExpiresAt: expiresAt}
}
