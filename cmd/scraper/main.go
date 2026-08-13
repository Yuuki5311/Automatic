// Command scraper 是交易猫看板统计抓取服务的主程序入口。
//
// 支持三种运行方式：
//   - 默认 / -once：执行一次看板抓取并写入本地 JSON 后退出；
//   - -daemon：常驻进程，仅按 config 中 scraper.cron_expr 定时执行（启动时不立即抓取）。
//   - -web：启用 Web 状态仪表盘（默认 http://127.0.0.1:8080）
//
// 所有浏览器操作均在 headless 模式下执行，不抢占鼠标。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/logger"
	"github.com/example/jiaoyimao-scraper/internal/pipeline"
	"github.com/example/jiaoyimao-scraper/internal/status"
	"github.com/example/jiaoyimao-scraper/internal/web"
)

func main() {
	configPath := flag.String("config", "./configs/config.yaml", "配置文件路径")
	once := flag.Bool("once", false, "仅运行一次后退出")
	daemon := flag.Bool("daemon", false, "以守护进程模式运行（定时任务）")
	webFlag := flag.Bool("web", false, "启用 Web 状态仪表盘")
	flag.Parse()

	// 1. 加载配置（logger 未初始化，用 fmt 输出致命错误）
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化结构化日志
	cleanup, err := logger.Init(logger.Config{
		Level:      cfg.Log.Level,
		File:       cfg.Log.File,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	// 3. 初始化浏览器管理器（headless模式）
	browserMgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		slog.Error("初始化浏览器失败", "component", "main", "error", err)
		os.Exit(1)
	}
	defer browserMgr.Close()

	// 4. 登录服务 + 验证码识别器
	loginSvc := &auth.LoginService{}
	captchaSolver := newCaptchaSolver(&cfg.Captcha)

	// 5. 账户库（从 config.JYM 迁移遗留单账号）+ 状态 + 看板抓取流程
	acctStore := accounts.NewStore(accounts.PathFromCookie(cfg.JYM.CookiePath))
	if err := acctStore.Load(); err != nil {
		slog.Error("加载账户失败", "component", "main", "error", err)
		os.Exit(1)
	}
	if _, err := acctStore.MigrateFromConfig(cfg.JYM); err != nil {
		slog.Warn("迁移遗留账户失败", "component", "main", "error", err)
	}

	st := status.NewStore()
	board := pipeline.NewBoardRun(cfg, browserMgr, loginSvc, captchaSolver, st, acctStore)
	runScrape := func() {
		if err := board.Run(); err != nil {
			slog.Warn("抓取未执行", "component", "main", "error", err)
		}
	}

	// 6. 启动 Web 仪表盘（可选）
	var webSrv *web.Server
	if *webFlag {
		webSrv, err = web.New(st, cfg, loginSvc, browserMgr, captchaSolver, acctStore)
		if err != nil {
			slog.Error("初始化Web仪表盘失败", "component", "main", "error", err)
		} else {
			webSrv.SetScrapeFunc(runScrape)
			go func() {
				slog.Info("Web仪表盘已启动", "component", "main", "addr", cfg.Web.Addr)
				if err := webSrv.ListenAndServe(cfg.Web.Addr); err != nil {
					slog.Error("Web仪表盘服务错误", "component", "main", "error", err)
				}
			}()
		}
	}

	if *daemon {
		st.SetDaemonState(status.DaemonRunning)
	}

	// 7. 执行模式分支
	if *once || (!*daemon) {
		if *webFlag && !*daemon {
			slog.Info("提示: -once 模式仪表盘仅在本次抓取期间可访问，常驻监控请用 -daemon -web", "component", "main")
		}
		runScrape()
		return
	}

	if *daemon {
		c := cron.New(cron.WithChain(cron.SkipIfStillRunning(logger.NewCronLogger())))
		if _, err := c.AddFunc(cfg.Scraper.CronExpr, runScrape); err != nil {
			slog.Error("无效的定时表达式", "component", "main", "expr", cfg.Scraper.CronExpr, "error", err)
			os.Exit(1)
		}

		c.Start()
		slog.Info("守护进程已启动", "component", "main", "cron", cfg.Scraper.CronExpr)

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		slog.Info("收到退出信号，正在关闭...", "component", "main")
		if webSrv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			webSrv.Shutdown(shutdownCtx)
		}
		st.SetDaemonState(status.DaemonStopped)
		c.Stop()
		return
	}

	// 默认：单次运行
	runScrape()
}

// newCaptchaSolver 根据配置选择验证码识别策略。
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
