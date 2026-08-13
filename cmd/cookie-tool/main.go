// cookie-tool 是交易猫 Cookie 管理 CLI 工具。
//
// 用法:
//
//	cookie-tool -login -config <配置文件路径>                               # 使用配置账号自动登录并保存 Cookie
//	cookie-tool -import <浏览器导出的Cookie JSON> -config <配置文件路径>  # 导入并校验
//	cookie-tool -check -config <配置文件路径>                              # 仅检查Cookie是否有效
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
)

func main() {
	configPath := flag.String("config", "./configs/config.yaml", "配置文件路径")
	importPath := flag.String("import", "", "从浏览器导出的Cookie JSON文件路径导入")
	doLogin := flag.Bool("login", false, "使用配置账号自动登录并保存 Cookie")
	checkOnly := flag.Bool("check", true, "检查Cookie是否有效（默认执行，可传 -check=false 跳过）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	if *doLogin {
		browserMgr, err := browser.NewManager(&cfg.Browser)
		if err != nil {
			fmt.Fprintf(os.Stderr, "初始化浏览器失败: %v\n", err)
			os.Exit(1)
		}
		defer browserMgr.Close()

		solver := newCaptchaSolver(&cfg.Captcha)
		ctx, cancel := browserMgr.NewContext(cfg.Browser.TimeoutSec)
		defer cancel()

		if err := performLoginAndSave(ctx, cfg, solver, (&auth.LoginService{}).PerformLogin, auth.SaveCookies); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("Cookie 登录成功！")
		fmt.Printf("保存位置: %s\n", cfg.JYM.CookiePath)
	}

	// 导入模式
	if *importPath != "" {
		data, err := os.ReadFile(*importPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取导入文件失败: %v\n", err)
			os.Exit(1)
		}
		if err := auth.ImportFromJSON(cfg.JYM.CookiePath, data); err != nil {
			fmt.Fprintf(os.Stderr, "导入Cookie失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Cookie 导入成功！")
		fmt.Printf("保存位置: %s\n", cfg.JYM.CookiePath)
	}

	// 检查模式
	if *checkOnly {
		cookies, err := auth.LoadCookies(cfg.JYM.CookiePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "加载Cookie失败: %v\n", err)
			os.Exit(1)
		}
		if auth.IsCookieValid(cookies) {
			fmt.Println("✓ Cookie 有效")
			fmt.Printf("  Cookie 数量: %d\n", len(cookies.Cookies))
			fmt.Printf("  过期时间: %s\n", cookies.ExpiresAt.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Println("✗ Cookie 无效或已过期，请重新获取")
			os.Exit(1)
		}
	}
}
