package main

import (
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/logger"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

//go:embed assets/*
var assets embed.FS

func main() {
	cfgPath := "./configs/config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}

	cleanup, err := logger.Init(logger.Config{
		Level:      cfg.Log.Level,
		File:       cfg.Log.File,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
	})
	if err != nil {
		slog.Error("初始化日志失败", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	st := status.NewStore()
	guiApp := NewApp(cfg, st)

	wa := application.New(application.Options{
		Name:        "交易猫数据抓取",
		Description: "交易猫商户工作台数据抓取与飞书同步",
		Services: []application.Service{
			application.NewService(guiApp),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
	})

	wa.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "交易猫数据抓取 · 状态面板",
		Width:     1100,
		Height:    780,
		MinWidth:  800,
		MinHeight: 600,
		URL:       "/",
	})

	wa.OnShutdown(func() {
		if guiApp.browserMgr != nil {
			guiApp.browserMgr.Close()
		}
	})

	if err := wa.Run(); err != nil {
		slog.Error("应用启动失败", "error", err)
		os.Exit(1)
	}
}
