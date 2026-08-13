package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

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
	cfgPath := ""
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	} else {
		exe, _ := os.Executable()
		cfgPath = filepath.Join(filepath.Dir(exe), "configs", "config.yaml")
		if _, err := os.Stat(cfgPath); err != nil {
			cfgPath = "./configs/config.yaml"
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	logCfg := logger.Config{
		Level: cfg.Log.Level, File: cfg.Log.File,
		MaxSizeMB: cfg.Log.MaxSizeMB, MaxBackups: cfg.Log.MaxBackups,
	}
	if logCfg.File == "" {
		logCfg.File = "./data/scraper.log"
	}
	cleanup, err := logger.Init(logCfg)
	if err != nil {
		panic(err)
	}
	defer cleanup()
	slog.Info("GUI应用启动", "config", cfgPath)

	browserMgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		slog.Error("初始化浏览器失败", "error", err)
	}

	var captchaSolver captcha.Solver
	switch cfg.Captcha.Provider {
	case "chaojiying", "2captcha":
		captchaSolver = captcha.NewThirdPartySolver(&cfg.Captcha)
	default:
		captchaSolver = captcha.NewSliderSolver(&cfg.Captcha)
	}

	loginSvc := &auth.LoginService{}
	st := status.NewStore()

	// 启动 HTTP 仪表盘（复用 web 包，含 / /api/status /api/login /api/cookies）
	webSrv, err := web.New(st, cfg, loginSvc, browserMgr, captchaSolver)
	if err != nil {
		panic(err)
	}

	scrapeFn := pipeline.NewBoardRun(cfg, browserMgr, loginSvc, captchaSolver, st).Run
	webSrv.SetScrapeFunc(scrapeFn)

	cronSched, err := startDaemonCron(cfg, st, scrapeFn)
	if err != nil {
		slog.Error("无效的定时表达式", "expr", cfg.Scraper.CronExpr, "error", err)
		os.Exit(1)
	}
	slog.Info("GUI 守护进程已启动", "cron", cfg.Scraper.CronExpr)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	go func() {
		slog.Info("仪表盘HTTP服务启动", "port", port)
		if err := webSrv.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			slog.Error("HTTP服务错误", "error", err)
		}
	}()

	// Wails 窗口加载 HTTP 仪表盘
	wa := application.New(application.Options{
		Name:        "交易猫数据抓取",
		Description: "交易猫商户工作台数据抓取与飞书同步",
	})

	wa.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:           "交易猫数据抓取 · 状态面板",
		Width:           1100,
		Height:          780,
		MinWidth:        800,
		MinHeight:       600,
		URL:             fmt.Sprintf("http://127.0.0.1:%d", port),
		DevToolsEnabled: true,
	})

	wa.OnShutdown(func() {
		st.SetDaemonState(status.DaemonStopped)
		if cronSched != nil {
			cronSched.Stop()
		}
		if browserMgr != nil {
			browserMgr.Close()
		}
	})

	if err := wa.Run(); err != nil {
		slog.Error("应用启动失败", "error", err)
		os.Exit(1)
	}
}
