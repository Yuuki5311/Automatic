package scraper

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

type browserScraper struct {
	cfg        *config.Config
	browserMgr *browser.Manager
	cookies    []models.CookieEntry
}

func newBrowserScraper(cfg *config.Config, mgr *browser.Manager, cookies []models.CookieEntry) *browserScraper {
	return &browserScraper{cfg: cfg, browserMgr: mgr, cookies: cookies}
}

func (b *browserScraper) setCookies(cookies []models.CookieEntry) {
	b.cookies = cookies
}

func (b *browserScraper) ScrapeRecyclePage(
	ctx context.Context,
	game config.GameConfig,
	tableIndex int,
) ([]models.RecycleOrder, error) {
	if b.browserMgr == nil {
		return nil, fmt.Errorf("浏览器管理器未配置，无法执行浏览器模式抓取")
	}
	if b.cfg == nil {
		return nil, fmt.Errorf("配置为空，无法执行浏览器模式抓取")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("上下文已取消: %w", err)
	}

	bctx, cancel := b.browserMgr.NewContext(b.cfg.Browser.TimeoutSec)
	defer cancel()

	workbenchURL := b.cfg.JYM.BaseURL + "/workbench"

	if err := browser.SetCookies(bctx, b.cookies); err != nil {
		return nil, fmt.Errorf("注入Cookie失败: %w", err)
	}
	if err := browser.Navigate(bctx, workbenchURL); err != nil {
		return nil, fmt.Errorf("导航到工作台失败: %w", err)
	}
	_ = browser.WaitReady(bctx)
	_ = browser.Sleep(bctx, 2*time.Second)
	pageURL, _ := browser.Location(bctx)
	if strings.Contains(pageURL, "/login") {
		return nil, fmt.Errorf("%w (当前URL: %s)", errLoginWall, pageURL)
	}

	if err := browser.WaitVisible(bctx, `.sidebar, .nav-left, [class*="sidebar"]`); err != nil {
		return nil, fmt.Errorf("等待侧栏失败: %w", err)
	}
	_ = browser.Click(bctx, `[class*="我的交易猫"]`)
	_ = browser.Sleep(bctx, time.Second)
	_ = browser.Click(bctx, `[class*="我的回收"]`)
	_ = browser.Sleep(bctx, time.Second)

	gameTabSelector := b.buildGameTabSelector(game)
	if err := browser.WaitVisible(bctx, gameTabSelector); err != nil {
		return nil, fmt.Errorf("等待游戏标签失败: %w", err)
	}
	_ = browser.Click(bctx, gameTabSelector)
	_ = browser.Sleep(bctx, 2*time.Second)
	_ = browser.WaitForNetworkIdle(bctx, 5*time.Second)

	if tableIndex > 0 {
		subTabSelector := b.buildSubTabSelector(game, tableIndex)
		if subTabSelector != "" {
			_ = browser.Click(bctx, subTabSelector)
			_ = browser.Sleep(bctx, time.Second)
			_ = browser.WaitForNetworkIdle(bctx, 5*time.Second)
		}
	}

	tableHTML, err := browser.ExtractHTML(bctx, `table, [class*="table"], .el-table`)
	if err != nil {
		return nil, fmt.Errorf("提取表格失败: %w", err)
	}

	orders := parseTableHTML(tableHTML, game.Name)
	log.Printf("[抓取-浏览器] %s (表格%d): 获取到 %d 条记录", game.Name, tableIndex, len(orders))
	return orders, nil
}

func (b *browserScraper) buildGameTabSelector(game config.GameConfig) string {
	selectors := []string{
		fmt.Sprintf(`[class*="%s"]`, game.Name),
	}
	if game.URL != "" {
		selectors = append(selectors, fmt.Sprintf(`[href*="%s"]`, strings.TrimPrefix(game.URL, "/")))
	}
	return strings.Join(selectors, ", ")
}

func (b *browserScraper) buildSubTabSelector(game config.GameConfig, index int) string {
	subTabs := map[string][]string{
		"原神":       {"官服", "渠道服"},
		"崩坏：星穹铁道": {"官服", "渠道服"},
	}
	if tabs, ok := subTabs[game.Name]; ok && index < len(tabs) {
		return fmt.Sprintf(`[class*="%s"], .sub-tab`, tabs[index])
	}
	return ""
}
