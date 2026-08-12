// Command scraper 是交易猫回收订单抓取服务的主程序入口。
//
// 支持三种运行方式：
//   - 默认 / -once：执行一次完整的抓取流程（刷新Cookie → 抓取全部游戏数据 → 写入飞书多维表格）后退出；
//   - -daemon：常驻进程，启动时立即执行一次，之后按 config 中 scraper.cron_expr 定时执行。
//
// 所有浏览器操作均在 headless 模式下执行，不抢占鼠标。
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/feishu"
	"github.com/example/jiaoyimao-scraper/internal/scraper"
)

func main() {
	configPath := flag.String("config", "./configs/config.yaml", "配置文件路径")
	once := flag.Bool("once", false, "仅运行一次后退出")
	daemon := flag.Bool("daemon", false, "以守护进程模式运行（定时任务）")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化浏览器管理器（headless模式，不显示窗口、不抢占鼠标）
	browserMgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		log.Fatalf("初始化浏览器失败: %v", err)
	}
	defer browserMgr.Close()

	// 初始化登录服务
	loginSvc := &auth.LoginService{}

	// 初始化验证码识别器
	captchaSolver := newCaptchaSolver(&cfg.Captcha)

	// 执行抓取的核心函数
	runScrape := func() {
		log.Println("========== 开始抓取 ==========")
		startTime := time.Now()

		// 1. 创建浏览器上下文（headless模式，所有操作在后端执行）
		ctx, cancel := browserMgr.NewContext(cfg.Browser.TimeoutSec)
		defer cancel()

		// 2. 检查Cookie有效性，无效则自动登录
		cookies, err := loginSvc.RefreshIfNeeded(ctx, cfg, captchaSolver)
		if err != nil {
			log.Printf("Cookie准备失败: %v", err)
			return
		}

		// 3. 抓取所有游戏数据
		scraperMgr := scraper.NewManager(cfg, browserMgr, cookies)
		allOrders, err := scraperMgr.ScrapeAll(ctx)
		if err != nil {
			log.Printf("抓取数据失败: %v", err)
			return
		}

		// 4. 写入飞书多维表格
		feishuClient := feishu.NewClient(&cfg.Feishu)
		for tableKey, orders := range allOrders {
			tableID, ok := cfg.Feishu.TableMapping[tableKey]
			if !ok {
				log.Printf("未找到表格 %s 的飞书映射，跳过", tableKey)
				continue
			}

			bitable := feishu.NewBitableOps(feishuClient, cfg.Feishu.BitableID)
			if err := bitable.BatchInsertOrders(ctx, tableID, orders); err != nil {
				log.Printf("写入飞书表格 %s 失败: %v", tableKey, err)
			}
		}

		elapsed := time.Since(startTime)
		log.Printf("========== 抓取完成 (耗时: %s) ==========", elapsed)
	}

	if *once {
		// 单次运行模式
		runScrape()
		return
	}

	if *daemon {
		// 守护进程模式：使用cron定时调度
		// 使用标准5字段表达式（与 configs/config.yaml.example 中 cron_expr 一致），
		// 并通过 SkipIfStillRunning 防止上一轮抓取尚未结束时触发重叠运行。
		c := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
		if _, err := c.AddFunc(cfg.Scraper.CronExpr, runScrape); err != nil {
			log.Fatalf("无效的定时表达式 %q: %v", cfg.Scraper.CronExpr, err)
		}

		// 启动时立即执行一次
		go runScrape()

		c.Start()
		log.Printf("守护进程已启动，定时表达式: %s", cfg.Scraper.CronExpr)

		// 等待退出信号
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("收到退出信号，正在关闭...")
		c.Stop()
		return
	}

	// 默认：单次运行
	runScrape()
}

// newCaptchaSolver 根据配置选择验证码识别策略。
//
// provider 为 "opencv" 时使用本地图像识别（SliderSolver），
// 否则回退到第三方打码平台（ThirdPartySolver）。
func newCaptchaSolver(cfg *config.CaptchaConfig) captcha.Solver {
	if cfg != nil && cfg.Provider == "opencv" {
		return captcha.NewSliderSolver(cfg)
	}
	return captcha.NewThirdPartySolver(cfg)
}
