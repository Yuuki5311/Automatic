package scraper

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// browserScraper 通过headless浏览器导航页面来提取数据。
// 作为API模式失败时的兜底，所有操作在headless模式下执行，不抢占鼠标。
type browserScraper struct {
	cfg        *config.Config
	browserMgr *browser.Manager
	cookies    []models.CookieEntry
}

func newBrowserScraper(cfg *config.Config, mgr *browser.Manager, cookies []models.CookieEntry) *browserScraper {
	return &browserScraper{cfg: cfg, browserMgr: mgr, cookies: cookies}
}

// ScrapeRecyclePage 通过浏览器模拟点击导航到回收页面并提取表格数据。
//
// 注意：
//   - 必须通过 browserMgr.NewContext 创建浏览器上下文。直接使用调用方传入的
//     裸 context 会因缺少 CDP executor 报 ErrInvalidContext（见 Task 5 结论）。
//   - 导航前注入持久化的登录 Cookie（browser.SetCookies），否则页面处于未登录态。
//   - 页面DOM结构属于推测值，接入真实站点时需按实际DOM调整选择器。
//     chromedp(ByQuery) 不支持 Playwright 风格的 :has-text() 伪类，
//     文本匹配导航需要额外的JS辅助封装（后续集成时实现）。
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

	// 新建独立浏览器标签页上下文（带超时），随函数退出关闭
	bctx, cancel := b.browserMgr.NewContext(b.cfg.Browser.TimeoutSec)
	defer cancel()

	workbenchURL := b.cfg.JYM.BaseURL + "/workbench"

	actions := []chromedp.Action{
		// 0. 注入登录Cookie（先于导航，确保会话有效）
		browser.SetCookies(b.cookies),
		// 1. 导航到工作台
		browser.Navigate(workbenchURL),
		browser.WaitReady(),
		browser.Sleep(2 * time.Second),
	}

	// 2. 点击左侧导航"我的交易猫"
	actions = append(actions,
		browser.WaitVisible(`.sidebar, .nav-left, [class*="sidebar"]`, chromedp.ByQuery),
		browser.Click(`[class*="我的交易猫"]`),
		browser.Sleep(1*time.Second),
	)

	// 3. 点击"我的回收"
	actions = append(actions,
		browser.Click(`[class*="我的回收"]`),
		browser.Sleep(1*time.Second),
	)

	// 4. 点击对应游戏标签
	gameTabSelector := b.buildGameTabSelector(game)
	actions = append(actions,
		browser.WaitVisible(gameTabSelector, chromedp.ByQuery),
		browser.Click(gameTabSelector),
		browser.Sleep(2*time.Second),
		browser.WaitForNetworkIdle(5*time.Second),
	)

	// 5. 如果该游戏有多个表格（tableIndex > 0），切换到对应子标签/分页
	if tableIndex > 0 {
		subTabSelector := b.buildSubTabSelector(game, tableIndex)
		if subTabSelector != "" {
			actions = append(actions,
				browser.Click(subTabSelector),
				browser.Sleep(1*time.Second),
				browser.WaitForNetworkIdle(5*time.Second),
			)
		}
	}

	// 6. 提取所有表格数据
	var tableHTML string
	actions = append(actions,
		browser.ExtractHTML(`table, [class*="table"], .el-table`, &tableHTML),
	)

	if err := chromedp.Run(bctx, actions...); err != nil {
		return nil, fmt.Errorf("浏览器操作失败: %w", err)
	}

	// 7. 解析HTML表格
	orders := parseTableHTML(tableHTML, game.Name)
	log.Printf("[抓取-浏览器] %s (表格%d): 获取到 %d 条记录", game.Name, tableIndex, len(orders))

	return orders, nil
}

// buildGameTabSelector 构建游戏标签选择器。
// 用 class 属性与链接 href（GameConfig.URL 对应的路径）双路匹配，
// 仅使用标准CSS选择器（chromedp ByQuery 不支持 :has-text 伪类）。
func (b *browserScraper) buildGameTabSelector(game config.GameConfig) string {
	selectors := []string{
		fmt.Sprintf(`[class*="%s"]`, game.Name),
	}
	if game.URL != "" {
		selectors = append(selectors, fmt.Sprintf(`[href*="%s"]`, strings.TrimPrefix(game.URL, "/")))
	}
	// 返回合法的CSS选择器列表（逗号分隔，任一匹配即命中）
	return strings.Join(selectors, ", ")
}

// buildSubTabSelector 构建子表格/分页的选择器。
// 例如原神可能分为"官服"和"渠道服"两个表格。
func (b *browserScraper) buildSubTabSelector(game config.GameConfig, index int) string {
	subTabs := map[string][]string{
		"原神":      {"官服", "渠道服"},
		"崩坏：星穹铁道": {"官服", "渠道服"},
	}
	if tabs, ok := subTabs[game.Name]; ok && index < len(tabs) {
		return fmt.Sprintf(`[class*="%s"], .sub-tab`, tabs[index])
	}
	return ""
}
