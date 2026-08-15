package main

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/accountsync"
	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/logger"
	"github.com/example/jiaoyimao-scraper/internal/leyoo"
	"github.com/example/jiaoyimao-scraper/internal/pipeline"
	"github.com/example/jiaoyimao-scraper/internal/scrapehistory"
	"github.com/example/jiaoyimao-scraper/internal/status"
	"github.com/example/jiaoyimao-scraper/internal/web"
)

func main() {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
		_ = os.Chdir(exeDir) // 双击启动时保证相对路径相对 exe 目录
	}

	cfgPath := ""
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	} else {
		cfgPath = filepath.Join("configs", "config.yaml")
		if exeDir != "" {
			p := filepath.Join(exeDir, "configs", "config.yaml")
			if _, err := os.Stat(p); err == nil {
				cfgPath = p
			}
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fatalf("加载配置失败: %v\n\n请确认与 gui.exe 同级存在 configs\\config.yaml", err)
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
		fatalf("初始化日志失败: %v", err)
	}
	defer cleanup()
	slog.Info("GUI应用启动", "config", cfgPath, "cwd", mustGetwd())

	browserMgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		fatalf("初始化浏览器失败: %v", err)
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

	acctStore := accounts.NewStore(accounts.PathFromCookie(cfg.JYM.CookiePath))
	if err := acctStore.Load(); err != nil {
		fatalf("加载账户失败: %v", err)
	}
	if _, err := acctStore.MigrateFromConfig(cfg.JYM); err != nil {
		slog.Warn("迁移遗留账户失败", "error", err)
	}

	slog.Info("启动前同步店铺 Cookie…")
	if n, err := accountsync.SyncExistingSuppliers(acctStore, leyoo.NewClient(""), accountsync.CryptoFromConfig(cfg)); err != nil {
		slog.Warn("启动同步店铺列表未完全成功", "kept", n, "error", err)
	} else {
		slog.Info("启动同步店铺 Cookie 完成", "kept", n)
	}

	webSrv, err := web.New(st, cfg, loginSvc, browserMgr, captchaSolver, acctStore)
	if err != nil {
		fatalf("初始化 Web 仪表盘失败: %v", err)
	}

	board := pipeline.NewBoardRun(cfg, browserMgr, loginSvc, captchaSolver, st, acctStore)
	acctPath := accounts.PathFromCookie(cfg.JYM.CookiePath)
	hist := scrapehistory.NewStore(scrapehistory.PathFromAccounts(acctPath))
	if err := hist.Load(); err != nil {
		slog.Warn("加载抓取历史失败", "error", err)
	}
	board.History = hist
	syncHist := func() {
		list := hist.List()
		out := make([]status.HistoryEntry, len(list))
		for i, e := range list {
			out[i] = status.HistoryEntry{ID: e.ID, At: e.At, Account: e.Account, Status: e.Status, Error: e.Error}
		}
		st.SetScrapeHistory(out)
	}
	syncHist()
	scrapeFn := func() {
		if err := board.Run(); err != nil {
			slog.Warn("抓取未执行", "error", err)
		}
	}
	webSrv.SetScrapeFunc(scrapeFn)
	webSrv.SetHistoryStore(hist)

	cronSched, err := startDaemonCron(cfg, st, scrapeFn)
	if err != nil {
		fatalf("无效的定时表达式 %q: %v\n请检查 configs\\config.yaml 里的 scraper.cron_expr", cfg.Scraper.CronExpr, err)
	}
	webSrv.SetConfigPath(cfgPath)
	webSrv.SetRescheduleFunc(cronSched.Reschedule)
	slog.Info("GUI 守护进程已启动", "cron", cfg.Scraper.CronExpr)

	addr, err := startLocalDashboard(webSrv)
	if err != nil {
		fatalf("启动本地仪表盘失败: %v", err)
	}

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
		URL:             "http://" + addr,
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
		msg := err.Error()
		hint := ""
		if strings.Contains(strings.ToLower(msg), "webview") || strings.Contains(msg, "WebView2") {
			hint = "\n\n常见原因：未安装 Microsoft Edge WebView2 运行时。\n请到微软官网安装「WebView2 Runtime」后重试。"
		}
		fatalf("应用窗口启动失败: %v%s", err, hint)
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// fatalf 打印错误、写入 startup_error.txt，并等待回车（避免双击时窗口一闪而过）。
func fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	_ = os.WriteFile("startup_error.txt", []byte(msg+"\n"), 0o644)
	fmt.Fprintln(os.Stderr, "\n（详情已写入 startup_error.txt）")
	fmt.Fprint(os.Stderr, "按回车键退出…")
	_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
	os.Exit(1)
}

// startLocalDashboard 绑定临时端口并在同一 listener 上 Serve，避免 Close 后再 ListenAndServe 的端口竞态。
func startLocalDashboard(srv *web.Server) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	go func() {
		slog.Info("仪表盘HTTP服务启动", "addr", addr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP服务错误", "error", err)
		}
	}()
	return addr, nil
}
