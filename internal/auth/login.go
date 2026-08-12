package auth

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

const (
	captchaPollTries    = 20
	captchaPollInterval = 500 * time.Millisecond
	captchaSolveWait    = 3 * time.Second
)

type LoginService struct{}

func (s *LoginService) PerformLogin(
	ctx context.Context,
	cfg *config.Config,
	captchaSolver captcha.Solver,
) (*models.CookieData, error) {
	loginURL := cfg.JYM.BaseURL + "/login"
	slog.Info("导航到登录页", "component", "login", "url", loginURL)

	if err := chromedp.Run(ctx,
		browser.Navigate(loginURL),
		browser.WaitReady(),
		browser.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("加载登录页失败: %w", err)
	}

	inputSelectors := s.detectLoginForm(cfg)
	slog.Info("使用账号密码登录", "component", "login")

	if err := chromedp.Run(ctx,
		browser.WaitVisible(inputSelectors.username, chromedp.ByQuery),
		browser.Input(inputSelectors.username, cfg.JYM.Username),
		browser.Sleep(500*time.Millisecond),
		browser.Input(inputSelectors.password, cfg.JYM.Password),
		browser.Sleep(500*time.Millisecond),
	); err != nil {
		return nil, fmt.Errorf("输入账号密码失败: %w", err)
	}

	if err := chromedp.Run(ctx,
		browser.Click(inputSelectors.submitBtn),
		browser.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("点击登录按钮失败: %w", err)
	}

	if err := s.waitAndSolveCaptcha(ctx, captchaSolver); err != nil {
		return nil, fmt.Errorf("验证码处理失败: %w", err)
	}

	if err := chromedp.Run(ctx,
		browser.WaitVisible(`.workbench, .main-content, [class*="layout"]`, chromedp.ByQuery),
		browser.Sleep(1*time.Second),
	); err != nil {
		return nil, fmt.Errorf("登录验证失败，未检测到工作台页面: %w", err)
	}

	cookieData, err := s.extractCookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("提取Cookie失败: %w", err)
	}

	slog.Info("登录成功", "component", "login", "cookie_count", len(cookieData.Cookies))
	return cookieData, nil
}

func (s *LoginService) RefreshIfNeeded(
	ctx context.Context,
	cfg *config.Config,
	captchaSolver captcha.Solver,
) (*models.CookieData, error) {
	existing, err := LoadCookies(cfg.JYM.CookiePath)
	if err == nil && IsCookieValid(existing) {
		slog.Info("Cookie有效，跳过登录", "component", "cookie")
		return existing, nil
	}

	slog.Warn("Cookie无效或过期，执行自动登录", "component", "cookie")
	newCookies, err := s.PerformLogin(ctx, cfg, captchaSolver)
	if err != nil {
		return nil, err
	}

	if err := SaveCookies(cfg.JYM.CookiePath, newCookies); err != nil {
		slog.Error("保存Cookie失败", "component", "cookie", "error", err)
	}

	return newCookies, nil
}

type loginFormSelectors struct {
	username  string
	password  string
	submitBtn string
	smsBtn    string
}

func (s *LoginService) detectLoginForm(cfg *config.Config) loginFormSelectors {
	if cfg.JYM.LoginType == "sms" {
		return loginFormSelectors{
			username:  `input[type="text"], input[placeholder*="手机"], input[name="phone"]`,
			password:  `input[placeholder*="验证码"], input[name="sms_code"]`,
			submitBtn: `button[type="submit"], .login-btn, [class*="login"] button`,
			smsBtn:    `.get-sms-code, [class*="sms"] button, .send-code`,
		}
	}
	return loginFormSelectors{
		username:  `input[type="text"], input[placeholder*="手机"], input[placeholder*="账号"], input[name="phone"]`,
		password:  `input[type="password"], input[placeholder*="密码"]`,
		submitBtn: `button[type="submit"], .login-btn, [class*="login"] button, form button`,
	}
}

func (s *LoginService) waitAndSolveCaptcha(ctx context.Context, solver captcha.Solver) error {
	return pollForCaptcha(ctx, solver, captchaPollTries, captchaPollInterval)
}

func pollForCaptcha(
	ctx context.Context,
	solver captcha.Solver,
	tries int,
	interval time.Duration,
) error {
	for i := 0; i < tries; i++ {
		detected, err := solver.Detect(ctx)
		if err != nil {
			slog.Warn("验证码检测出错", "component", "login", "error", err)
			continue
		}
		if detected {
			slog.Info("检测到验证码，开始自动解决", "component", "login", "captcha_type", solver.Type())
			if err := solver.Solve(ctx); err != nil {
				return fmt.Errorf("%s验证码解决失败: %w", solver.Type(), err)
			}
			slog.Info("验证码已解决", "component", "login")
			if err := chromedp.Run(ctx, browser.Sleep(captchaSolveWait)); err != nil {
				return fmt.Errorf("验证码解决后等待页面跳转失败: %w", err)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("等待验证码超时或上下文取消: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
	slog.Info("未检测到验证码，继续流程", "component", "login")
	return nil
}

func (s *LoginService) extractCookies(ctx context.Context) (*models.CookieData, error) {
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &currentURL)); err != nil {
		return nil, fmt.Errorf("获取当前URL失败: %w", err)
	}

	var cdpCookies []*network.Cookie
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cdpCookies, err = network.GetCookies().WithURLs([]string{currentURL}).Do(ctx)
		return err
	})); err != nil {
		return nil, fmt.Errorf("CDP获取Cookie失败: %w", err)
	}

	return cookiesToData(cdpCookies, time.Now()), nil
}

func cookiesToData(cdpCookies []*network.Cookie, now time.Time) *models.CookieData {
	entries := make([]models.CookieEntry, 0, len(cdpCookies))
	var maxExpires float64
	for _, c := range cdpCookies {
		if c == nil {
			continue
		}
		entry := models.CookieEntry{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
		}
		entries = append(entries, entry)
		if c.Expires > maxExpires {
			maxExpires = c.Expires
		}
	}

	expiresAt := now.Add(24 * time.Hour)
	if maxExpires > 0 {
		expiresAt = time.Unix(int64(maxExpires), 0)
	}

	return &models.CookieData{
		Cookies:   entries,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
	}
}
