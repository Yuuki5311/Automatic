package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/feishu"
	"github.com/example/jiaoyimao-scraper/internal/models"
	"github.com/example/jiaoyimao-scraper/internal/scraper"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

// App Wails 绑定的应用结构体，前端通过 window.go.main.App 调用其方法。
type App struct {
	cfg           *config.Config
	store         *status.Store
	browserMgr    *browser.Manager
	loginSvc      *auth.LoginService
	captchaSolver captcha.Solver
}

// NewApp 创建 App 实例并初始化浏览器等服务。
func NewApp(cfg *config.Config, st *status.Store) *App {
	browserMgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		slog.Error("初始化浏览器失败", "error", err)
	}

	captchaSolver := newCaptchaSolver(&cfg.Captcha)

	return &App{
		cfg:           cfg,
		store:         st,
		browserMgr:    browserMgr,
		loginSvc:      &auth.LoginService{},
		captchaSolver: captchaSolver,
	}
}

func newCaptchaSolver(cfg *config.CaptchaConfig) captcha.Solver {
	if cfg == nil {
		return captcha.NewSliderSolver(&config.CaptchaConfig{MaxRetry: 3})
	}
	switch cfg.Provider {
	case "chaojiying", "2captcha":
		return captcha.NewThirdPartySolver(cfg)
	default:
		return captcha.NewSliderSolver(cfg)
	}
}

// ====== 前端可调用的方法 ======

// GetStatus 返回当前状态快照。
func (a *App) GetStatus() status.Snapshot {
	return a.store.Snapshot()
}

// TriggerLogin 触发自动登录（异步执行，通过 GetStatus 轮询进度）。
func (a *App) TriggerLogin() string {
	snap := a.store.Snapshot()
	if snap.LoginPhase == status.LoginRunning {
		return "already_running"
	}
	if a.browserMgr == nil {
		a.store.SetLoginPhase(status.LoginFailed, "浏览器未初始化")
		return "browser_error"
	}

	a.store.SetLoginPhase(status.LoginRunning, "")

	go func() {
		ctx, cancel := a.browserMgr.NewContext(a.cfg.Browser.TimeoutSec)
		defer cancel()

		cookies, err := a.loginSvc.PerformLogin(ctx, a.cfg, a.captchaSolver)
		if err != nil {
			slog.Error("UI触发登录失败", "error", err)
			a.store.SetLoginPhase(status.LoginFailed, err.Error())
			return
		}

		if err := auth.SaveCookies(a.cfg.JYM.CookiePath, cookies); err != nil {
			slog.Error("保存Cookie失败", "error", err)
		}
		a.store.SetCookie(cookies, auth.IsCookieValid(cookies))
		a.store.SetLoginPhase(status.LoginSuccess, "")
	}()

	return "started"
}

// ImportCookies 手动导入 Cookie（EditThisCookie JSON 格式）。
func (a *App) ImportCookies(jsonStr string) map[string]interface{} {
	if err := auth.ImportFromJSON(a.cfg.JYM.CookiePath, []byte(jsonStr)); err != nil {
		return map[string]interface{}{"status": "error", "message": err.Error()}
	}

	cookies, err := auth.LoadCookies(a.cfg.JYM.CookiePath)
	if err == nil {
		a.store.SetCookie(cookies, auth.IsCookieValid(cookies))
	}
	return map[string]interface{}{"status": "ok", "count": len(cookies.Cookies)}
}

// RunScrape 触发一次完整抓取（异步执行）。
func (a *App) RunScrape() string {
	snap := a.store.Snapshot()
	if snap.LoginPhase == status.LoginRunning {
		return "login_in_progress"
	}
	if snap.Phase != status.PhaseIdle {
		return "scrape_in_progress"
	}
	if a.browserMgr == nil {
		return "browser_error"
	}

	go a.runScrape()
	return "started"
}

// runScrape 执行完整的"登录 → 抓取 → 飞书同步"流程。
func (a *App) runScrape() {
	slog.Info("========== 开始抓取 ==========")
	a.store.RunStarted()

	ctx, cancel := a.browserMgr.NewContext(a.cfg.Browser.TimeoutSec)
	defer cancel()

	// 1. Cookie
	a.store.SetPhase(status.PhaseCookie)
	cookies, err := a.loginSvc.RefreshIfNeeded(ctx, a.cfg, a.captchaSolver)
	if err != nil {
		slog.Error("Cookie准备失败", "error", err)
		a.store.RunFinished(err, 0)
		return
	}
	a.store.SetCookie(cookies, auth.IsCookieValid(cookies))

	// 2. 抓取
	a.store.SetPhase(status.PhaseScraping)
	scraperMgr := scraper.NewManager(a.cfg, a.browserMgr, cookies)

	scraperMgr.SetSessionRefresher(func(refreshCtx context.Context) (*models.CookieData, error) {
		newC, refreshErr := a.loginSvc.RefreshIfNeeded(refreshCtx, a.cfg, a.captchaSolver)
		if refreshErr == nil {
			a.store.SetCookie(newC, auth.IsCookieValid(newC))
		}
		return newC, refreshErr
	})

	scraperMgr.SetResultReporter(func(tableKey string, count int, reportErr error) {
		a.store.RecordGame(tableKey, count, reportErr)
	})

	allOrders, err := scraperMgr.ScrapeAll(ctx)
	if err != nil {
		slog.Error("抓取数据失败", "error", err)
		a.store.RunFinished(err, 0)
		return
	}

	// 3. 飞书同步
	a.store.SetPhase(status.PhaseSyncing)
	feishuClient := feishu.NewClient(&a.cfg.Feishu)
	totalOrders := 0
	syncErrCount := 0
	for tableKey, orders := range allOrders {
		tableID, ok := a.cfg.Feishu.TableMapping[tableKey]
		if !ok {
			slog.Warn("未找到表格映射", "table", tableKey)
			continue
		}
		bitable := feishu.NewBitableOps(feishuClient, a.cfg.Feishu.BitableID)
		newC, updC, syncErr := bitable.BatchInsertOrders(ctx, tableID, orders)
		if syncErr != nil {
			slog.Error("写入飞书表格失败", "table", tableKey, "error", syncErr)
			syncErrCount++
		}
		a.store.RecordGameSync(tableKey, status.FeishuSyncResult{NewCount: newC, UpdCount: updC, Err: syncErr})
		totalOrders += len(orders)
	}

	var runErr error
	if syncErrCount > 0 {
		runErr = fmt.Errorf("%d 个飞书表格同步失败", syncErrCount)
	}
	a.store.RunFinished(runErr, totalOrders)
	slog.Info("========== 抓取完成 ==========")
}
