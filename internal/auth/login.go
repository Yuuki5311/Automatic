// Package auth 提供登录相关的 Cookie 持久化与管理能力。
//
// LoginService 实现交易猫后台的自动登录流程：导航到登录页、智能检测登录表单、
// 输入账号密码、轮询处理滑块验证码（可能延迟 1-3 秒出现）、通过 CDP 协议提取
// 全部 Cookie（包括 HTTPOnly）。所有浏览器操作均在 headless 模式下执行，不抢占
// 用户鼠标。Cookie 到期后可通过 RefreshIfNeeded 自动重新登录刷新。
package auth

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// 验证码轮询与登录流程相关的时间参数。
const (
	// captchaPollTries 验证码轮询次数（每次间隔 500ms，总计约 10 秒）。
	// 滑块验证码通常在点击登录按钮后延迟 1-3 秒才出现。
	captchaPollTries = 20
	// captchaPollInterval 每次轮询的间隔。
	captchaPollInterval = 500 * time.Millisecond
	// captchaSolveWait 验证码解决后等待页面跳转的时间。
	captchaSolveWait = 3 * time.Second
)

// LoginService 自动登录服务。
//
// 空结构体即可使用，无需初始化状态；登录所需的浏览器上下文、配置与
// 验证码识别器均由调用方通过方法参数传入。
type LoginService struct{}

// PerformLogin 执行自动登录流程。
//
// 流程：导航到登录页 → 智能检测登录表单并输入账号密码 → 点击登录按钮 →
// 轮询检测并解决滑块验证码 → 等待跳转到工作台页面 → 通过 CDP 提取全部
// Cookie（包括 HTTPOnly）。所有操作在 headless 浏览器中执行，不抢占用户鼠标。
func (s *LoginService) PerformLogin(
	ctx context.Context,
	cfg *config.Config,
	captchaSolver captcha.Solver,
) (*models.CookieData, error) {
	loginURL := cfg.JYM.BaseURL + "/login"
	log.Printf("[登录] 导航到登录页: %s", loginURL)

	// 1. 导航到登录页
	if err := chromedp.Run(ctx,
		browser.Navigate(loginURL),
		browser.WaitReady(),
		browser.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("加载登录页失败: %w", err)
	}

	// 2. 智能检测登录表单并输入用户名和密码
	inputSelectors := s.detectLoginForm(cfg)
	log.Printf("[登录] 使用%s登录", loginTypeLabel(cfg.JYM.LoginType))

	if err := chromedp.Run(ctx,
		browser.WaitVisible(inputSelectors.username, chromedp.ByQuery),
		browser.Input(inputSelectors.username, cfg.JYM.Username),
		browser.Sleep(500*time.Millisecond),
		browser.Input(inputSelectors.password, cfg.JYM.Password),
		browser.Sleep(500*time.Millisecond),
	); err != nil {
		return nil, fmt.Errorf("输入账号密码失败: %w", err)
	}

	// 3. 点击登录按钮
	if err := chromedp.Run(ctx,
		browser.Click(inputSelectors.submitBtn),
		browser.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("点击登录按钮失败: %w", err)
	}

	// 4. 轮询检测并处理滑块验证码（可能延迟出现）
	if err := s.waitAndSolveCaptcha(ctx, captchaSolver); err != nil {
		return nil, fmt.Errorf("验证码处理失败: %w", err)
	}

	// 5. 等待登录成功（检查是否跳转到工作台页面）
	if err := chromedp.Run(ctx,
		browser.WaitVisible(`.workbench, .main-content, [class*="layout"]`, chromedp.ByQuery),
		browser.Sleep(1*time.Second),
	); err != nil {
		return nil, fmt.Errorf("登录验证失败，未检测到工作台页面: %w", err)
	}

	// 6. 通过CDP协议提取所有Cookie（包括HTTPOnly）
	cookieData, err := s.extractCookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("提取Cookie失败: %w", err)
	}

	log.Printf("[登录] 登录成功，提取到 %d 个Cookie", len(cookieData.Cookies))
	return cookieData, nil
}

// RefreshIfNeeded 检查Cookie是否有效，无效时重新登录。
//
// Cookie 有效时直接返回已有 Cookie 数据（跳过登录）；Cookie 缺失、
// 过期或缺少关键登录态 Cookie 时，调用 PerformLogin 重新登录，
// 并在登录成功后通过 SaveCookies 持久化。
func (s *LoginService) RefreshIfNeeded(
	ctx context.Context,
	cfg *config.Config,
	captchaSolver captcha.Solver,
) (*models.CookieData, error) {
	existing, err := LoadCookies(cfg.JYM.CookiePath)
	if err == nil && IsCookieValid(existing) {
		log.Printf("[Cookie] Cookie有效，跳过登录")
		return existing, nil
	}

	log.Printf("[Cookie] Cookie无效或过期，执行自动登录")
	newCookies, err := s.PerformLogin(ctx, cfg, captchaSolver)
	if err != nil {
		return nil, err
	}

	if err := SaveCookies(cfg.JYM.CookiePath, newCookies); err != nil {
		log.Printf("[Cookie] 保存Cookie失败: %v", err)
	}

	return newCookies, nil
}

// loginFormSelectors 登录表单元素选择器集合。
type loginFormSelectors struct {
	username  string
	password  string
	submitBtn string
	smsBtn    string // 获取短信验证码按钮（仅短信登录模式）
}

// detectLoginForm 返回登录表单选择器（根据配置决定使用哪种登录方式）。
//
// 交易猫通常支持：手机号+密码 / 手机号+短信验证码，默认使用密码登录方式。
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

// loginTypeLabel 返回登录方式的中文描述（仅用于日志）。
func loginTypeLabel(loginType string) string {
	if loginType == "sms" {
		return "短信验证码登录"
	}
	return "账号密码登录"
}

// waitAndSolveCaptcha 轮询等待并解决验证码（验证码可能延迟1-3秒出现）。
//
// 最多轮询 captchaPollTries 次（约 10 秒）；10 秒内未检测到验证码时视为
// 无需验证码，直接继续后续流程。轮询期间会响应上下文取消。
func (s *LoginService) waitAndSolveCaptcha(ctx context.Context, solver captcha.Solver) error {
	return pollForCaptcha(ctx, solver, captchaPollTries, captchaPollInterval)
}

// pollForCaptcha 轮询检测验证码的核心逻辑。
//
// tries 与 interval 为轮询参数（由调用方注入，便于测试缩短轮询时间）。
// 检测到验证码时调用 Solve 解决；tries 次内始终未检测到则返回 nil 继续流程。
func pollForCaptcha(
	ctx context.Context,
	solver captcha.Solver,
	tries int,
	interval time.Duration,
) error {
	for i := 0; i < tries; i++ {
		detected, err := solver.Detect(ctx)
		if err != nil {
			log.Printf("[登录] 验证码检测出错: %v", err)
			continue
		}
		if detected {
			log.Printf("[登录] 检测到%s验证码，开始自动解决", solver.Type())
			if err := solver.Solve(ctx); err != nil {
				return fmt.Errorf("%s验证码解决失败: %w", solver.Type(), err)
			}
			log.Printf("[登录] 验证码已解决")
			// 验证通过后等待页面跳转
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
	// 轮询结束未检测到验证码，可能不需要验证码
	log.Printf("[登录] 未检测到验证码，继续流程")
	return nil
}

// extractCookies 通过CDP Network.GetCookies 提取浏览器中所有Cookie（包括HTTPOnly）。
func (s *LoginService) extractCookies(ctx context.Context) (*models.CookieData, error) {
	// 获取当前页面的URL作为cookie domain
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &currentURL)); err != nil {
		return nil, fmt.Errorf("获取当前URL失败: %w", err)
	}

	// 通过CDP协议获取所有Cookie（包括HTTPOnly的）。
	// 注意：CDP executor 只在 chromedp.Run 内部附加到 context，必须用 ActionFunc 包裹，
	// 否则直接调用 network.GetCookies().Do(ctx) 会返回 ErrInvalidContext。
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

// cookiesToData 将CDP协议返回的Cookie列表转换为持久化格式。
//
// 会话级Cookie（Expires 为 -1）不会计入最长有效期；存在有效过期时间的
// Cookie 时统一按其中最晚的过期时间计算，否则默认 24 小时。
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
		// 会话级Cookie的Expires为-1，忽略不计
		if c.Expires > maxExpires {
			maxExpires = c.Expires
		}
	}

	expiresAt := now.Add(24 * time.Hour) // 默认24小时
	if maxExpires > 0 {
		expiresAt = time.Unix(int64(maxExpires), 0)
	}

	return &models.CookieData{
		Cookies:   entries,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
	}
}
