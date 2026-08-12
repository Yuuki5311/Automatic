// Package scraper 提供回收订单数据抓取引擎。
//
// 支持两种抓取模式：
//   - API模式（internal/scraper/api.go）：直接调用反向工程出的内部API，
//     高效但依赖接口可用性。
//   - 浏览器模式（internal/scraper/browser.go）：headless Chrome 导航页面
//     提取表格数据，不抢占鼠标，作为API失败时的兜底。
//
// Manager 按配置中的 mode（api | browser | auto）编排两种模式：
// auto 模式下先尝试API，失败或返回空数据时自动切换到浏览器兜底。
package scraper

import (
	"context"
	"fmt"
	"log"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// Manager 抓取管理器，协调API和浏览器两种模式。
type Manager struct {
	cfg       *config.Config
	cookies   *models.CookieData
	apiClient *apiClient
	browserS  *browserScraper
}

// NewManager 创建抓取管理器。
//
// cfg 为 nil 时视为配置缺失，API与浏览器模式均不可用（ScrapeGameTable
// 会返回明确错误）；cookies 为 nil 时按空Cookie处理（API不带Cookie，
// 浏览器模式不注入会话，抓取大概率因未登录失败，由上层刷新Cookie后重试）。
func NewManager(cfg *config.Config, browserMgr *browser.Manager, cookies *models.CookieData) *Manager {
	var cookieEntries []models.CookieEntry
	if cookies != nil {
		cookieEntries = cookies.Cookies
	}

	m := &Manager{
		cfg:     cfg,
		cookies: cookies,
	}
	if cfg != nil {
		m.apiClient = newAPIClient(cfg.JYM.BaseURL, cookieEntries)
	}
	m.browserS = newBrowserScraper(cfg, browserMgr, cookieEntries)
	return m
}

// ScrapeAll 抓取所有配置中游戏的所有表格数据。
//
// 返回的 map 键规则：
//   - 单表格游戏：游戏名（如 "原神"）
//   - 多表格游戏：游戏名_tableN，N从1开始（如 "原神_table1"、"原神_table2"）
//
// 单个表格抓取失败只记录日志并继续，不中断整体流程。
func (m *Manager) ScrapeAll(ctx context.Context) (map[string][]models.RecycleOrder, error) {
	result := make(map[string][]models.RecycleOrder)

	if m.cfg == nil {
		return nil, fmt.Errorf("配置为空，无法抓取")
	}

	for _, game := range m.cfg.Scraper.Games {
		for i := 0; i < game.TableCount; i++ {
			tableKey := game.Name
			if game.TableCount > 1 {
				tableKey = fmt.Sprintf("%s_table%d", game.Name, i+1)
			}

			orders, err := m.ScrapeGameTable(ctx, game, i)
			if err != nil {
				log.Printf("[抓取] %s 表格%d 失败: %v", game.Name, i, err)
				continue
			}
			result[tableKey] = orders
			log.Printf("[抓取] %s: %d 条记录", tableKey, len(orders))
		}
	}

	return result, nil
}

// ScrapeGame 抓取单个游戏的全部表格数据（合并返回）。
// TableCount <= 0 时按单表格处理。任一表格失败立即返回错误。
func (m *Manager) ScrapeGame(ctx context.Context, game config.GameConfig) ([]models.RecycleOrder, error) {
	if m.cfg == nil {
		return nil, fmt.Errorf("配置为空，无法抓取")
	}
	count := game.TableCount
	if count <= 0 {
		count = 1
	}

	var all []models.RecycleOrder
	for i := 0; i < count; i++ {
		orders, err := m.ScrapeGameTable(ctx, game, i)
		if err != nil {
			return nil, fmt.Errorf("游戏 %s 表格%d 抓取失败: %w", game.Name, i, err)
		}
		all = append(all, orders...)
	}
	return all, nil
}

// ScrapeGameTable 抓取单个游戏的单个表格。
//
// 策略按配置 mode 决定：
//   - "api"：仅API模式，失败直接返回错误
//   - "browser"：仅浏览器模式
//   - "auto"（默认）：先尝试API，失败或返回空列表时用浏览器兜底
func (m *Manager) ScrapeGameTable(ctx context.Context, game config.GameConfig, tableIndex int) ([]models.RecycleOrder, error) {
	if m.cfg == nil {
		return nil, fmt.Errorf("配置为空，无法抓取")
	}
	mode := m.cfg.Scraper.Mode
	if mode == "" {
		mode = "auto"
	}

	switch mode {
	case "api":
		// 仅API模式：错误原样返回（如 Cookie已过期），不静默吞掉
		return m.scrapeViaAPI(ctx, game, tableIndex)
	case "browser":
		return m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
	case "auto":
		orders, err := m.scrapeViaAPI(ctx, game, tableIndex)
		if err == nil && len(orders) > 0 {
			return orders, nil
		}
		reason := "未知原因"
		if err != nil {
			reason = err.Error()
		} else {
			reason = "返回空数据"
		}
		log.Printf("[抓取] API获取 %s 表格%d 失败: %s, 切换到浏览器模式", game.Name, tableIndex, reason)
		return m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
	default:
		return nil, fmt.Errorf("未知抓取模式: %s", mode)
	}
}

// scrapeViaAPI 探测并调用API抓取单个表格。
func (m *Manager) scrapeViaAPI(ctx context.Context, game config.GameConfig, tableIndex int) ([]models.RecycleOrder, error) {
	if m.apiClient == nil {
		return nil, fmt.Errorf("API客户端未初始化（配置缺失）")
	}
	if err := m.apiClient.ProbeAPI(ctx); err != nil {
		return nil, fmt.Errorf("API探测失败: %w", err)
	}
	return m.apiClient.FetchRecycleOrders(ctx, game.Name, tableIndex)
}
