package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	loginURL := cfg.JYM.LoginURL
	if loginURL == "" {
		loginURL = cfg.JYM.BaseURL + "/workbench"
	}
	slog.Info("导航到登录页", "component", "login", "url", loginURL)

	if err := chromedp.Run(ctx,
		browser.Navigate(loginURL),
		browser.WaitReady(),
		browser.Sleep(8*time.Second),
	); err != nil {
		return nil, fmt.Errorf("加载登录页失败: %w", err)
	}

	// 等 SPA 渲染
	slog.Info("等待SPA渲染", "component", "login")
	for i := 0; i < 20; i++ {
		var bodyText string
		chromedp.Run(ctx, chromedp.Evaluate(`document.body ? document.body.innerText.substring(0,200) : ""`, &bodyText))
		if len(bodyText) > 20 {
			slog.Info("SPA已渲染", "component", "login", "body_preview", bodyText[:min(100, len(bodyText))])
			break
		}
		select {
		case <-ctx.Done(): return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}

	// 步骤1：点击"密码登录"tab
	clickPwdJS := `(()=>{
		for(const el of document.querySelectorAll('*')){
			if(el.childNodes.length===1 && el.textContent.trim()==='密码登录'){el.click();return true}
		}
		return false
	})()`
	var clicked bool
	for i := 0; i < 5; i++ {
		chromedp.Run(ctx, chromedp.Evaluate(clickPwdJS, &clicked))
		if clicked { break }
		chromedp.Run(ctx, browser.Sleep(time.Second))
	}
	slog.Info("密码登录tab", "component", "login", "clicked", clicked)

	// 步骤2：填写手机号和密码
	user, pass := cfg.JYM.Username, cfg.JYM.Password
	fillJS := fmt.Sprintf(`(()=>{
		const ph=document.querySelector('input[type="text"],input[type="tel"],input[placeholder*="手机"],input[placeholder*="账号"],input[name="phone"]');
		const pw=document.querySelector('input[type="password"],input[placeholder*="密码"]');
		if(!ph||!pw)return false;
		// 触发 React/Vue 受控组件的原生 value setter
		const nativeSetter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
		nativeSetter.call(ph,%q);ph.dispatchEvent(new Event('input',{bubbles:true}));
		nativeSetter.call(pw,%q);pw.dispatchEvent(new Event('input',{bubbles:true}));
		ph.dispatchEvent(new Event('change',{bubbles:true}));
		pw.dispatchEvent(new Event('change',{bubbles:true}));
		return true;
	})()`, user, pass)

	var filled bool
	for i := 0; i < 5; i++ {
		chromedp.Run(ctx, chromedp.Evaluate(fillJS, &filled))
		if filled { break }
		chromedp.Run(ctx, browser.Sleep(2*time.Second))
	}
	if !filled {
		return nil, fmt.Errorf("未找到登录表单输入框")
	}
	slog.Info("表单已填写", "component", "login")

	// 步骤3：按 Enter 键提交 + 点击登录按钮
	submitJS := `(()=>{
		const pw=document.querySelector('input[type="password"],input[placeholder*="密码"]');
		if(pw){
			pw.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',code:'Enter',keyCode:13,bubbles:true}));
			pw.dispatchEvent(new KeyboardEvent('keyup',{key:'Enter',code:'Enter',keyCode:13,bubbles:true}));
		}
		for(const el of document.querySelectorAll('button')){
			if(el.textContent.includes('登录')&&!el.textContent.includes('注册')){el.click();return true}
		}
		return !!pw;
	})()`
	var submitted bool
	chromedp.Run(ctx, chromedp.Evaluate(submitJS, &submitted))
	slog.Info("表单已提交", "component", "login", "submitted", submitted)
	// 诊断：等 3 秒后获取页面文本，检查是否有错误提示
	chromedp.Run(ctx, browser.Sleep(3*time.Second))
	var bodyText string
	chromedp.Run(ctx, chromedp.Evaluate(`document.body ? document.body.innerText : ""`, &bodyText))
	slog.Info("提交后页面内容", "component", "login", "body", bodyText[:min(500, len(bodyText))])

	// 等跳转（最多 30 秒）
	slog.Info("等待登录跳转", "component", "login")
	for i := 0; i < 30; i++ {
		var u string
		chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &u))
		if !strings.Contains(u, "login") && strings.Contains(u, "merchant") {
			slog.Info("已跳转到工作台", "component", "login", "url", u)
			break
		}
		select {
		case <-ctx.Done(): return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}

	// 验证码
	s.waitAndSolveCaptcha(ctx, captchaSolver)

	// 提取 Cookie
	var currentURL string
	chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &currentURL))
	slog.Info("登录后URL", "component", "login", "url", currentURL)

	cookieData, err := s.extractCookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("提取Cookie失败: %w", err)
	}

	// 如果还在登录页，手动导航到工作台
	if strings.Contains(currentURL, "login") {
		slog.Info("未自动跳转，手动导航到工作台", "component", "login")
		if err := chromedp.Run(ctx,
			browser.Navigate(cfg.JYM.BaseURL+"/workbench"),
			browser.WaitReady(),
			browser.Sleep(3*time.Second),
		); err != nil {
			return nil, fmt.Errorf("导航到工作台失败: %w", err)
		}
		var finalURL string
		chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &finalURL))
		if strings.Contains(finalURL, "login") {
			return nil, fmt.Errorf("登录后仍被重定向到登录页: %s", finalURL)
		}
		slog.Info("工作台已加载", "component", "login", "url", finalURL)
		cookieData, err = s.extractCookies(ctx)
		if err != nil {
			return nil, fmt.Errorf("提取工作台Cookie失败: %w", err)
		}
	}

	slog.Info("登录完成", "component", "login", "cookie_count", len(cookieData.Cookies))
	return cookieData, nil
}

func (s *LoginService) RefreshIfNeeded(ctx context.Context, cfg *config.Config, captchaSolver captcha.Solver) (*models.CookieData, error) {
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

func (s *LoginService) waitAndSolveCaptcha(ctx context.Context, solver captcha.Solver) error {
	return pollForCaptcha(ctx, solver, captchaPollTries, captchaPollInterval)
}

func pollForCaptcha(ctx context.Context, solver captcha.Solver, tries int, interval time.Duration) error {
	for i := 0; i < tries; i++ {
		detected, err := solver.Detect(ctx)
		if err != nil { slog.Warn("验证码检测出错", "component", "login", "error", err); continue }
		if detected {
			slog.Info("检测到验证码", "component", "login", "type", solver.Type())
			if err := solver.Solve(ctx); err != nil {
				return fmt.Errorf("验证码解决失败: %w", err)
			}
			slog.Info("验证码已解决", "component", "login")
			chromedp.Run(ctx, browser.Sleep(captchaSolveWait))
			return nil
		}
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-time.After(interval):
		}
	}
	slog.Info("未检测到验证码", "component", "login")
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
		if c == nil { continue }
		entry := models.CookieEntry{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Expires: c.Expires, HTTPOnly: c.HTTPOnly, Secure: c.Secure}
		entries = append(entries, entry)
		if c.Expires > maxExpires { maxExpires = c.Expires }
	}
	expiresAt := now.Add(24 * time.Hour)
	if maxExpires > 0 { expiresAt = time.Unix(int64(maxExpires), 0) }
	return &models.CookieData{Cookies: entries, UpdatedAt: now, ExpiresAt: expiresAt}
}
