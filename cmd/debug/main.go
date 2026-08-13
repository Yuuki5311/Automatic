// 简易调试入口：用 Rod 无头打开工作台并截图。
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
)

func main() {
	cfg, err := config.Load("./configs/config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg.Browser.Headless = true

	mgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer mgr.Close()

	ctx, cancel := mgr.NewContext(cfg.Browser.TimeoutSec)
	defer cancel()

	if err := browser.Navigate(ctx, "https://merchant.jiaoyimao.com/workbench"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = browser.Sleep(ctx, 5*time.Second)
	url, _ := browser.Location(ctx)
	fmt.Println("url:", url)
	if err := browser.Screenshot(ctx, "data/debug-shot.png"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("saved data/debug-shot.png")
}
