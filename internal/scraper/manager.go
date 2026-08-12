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
	"errors"
	"fmt"
	"log"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// errLoginWall 页面被重定向到登录页（浏览器模式会话已失效）。
//
// browserScraper 检测到登录墙时返回该哨兵错误，Manager 据此触发
// 重新登录刷新Cookie后用新Cookie重试一次。
var errLoginWall = errors.New("页面跳转到登录页，会话已失效")

// sessionRefresher 重新登录并返回新Cookie的回调（由调用方注入，
// 通常包装 auth.LoginService.RefreshIfNeeded）。
type sessionRefresher func(ctx context.Context) (*models.CookieData, error)

// Manager 抓取管理器，协调API和浏览器两种模式。
type Manager struct {
	cfg       *config.Config
	cookies   *models.CookieData
	apiClient *apiClient
	browserS  *browserScraper
	refresh   sessionRefresher // 会话失效时重新登录刷新Cookie
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

// SetSessionRefresher 注册会话刷新回调。
//
// 抓取过程中遇到会话失效（API返回401/403、浏览器模式页面跳转登录页）时，
// Manager 调用该回调重新登录并刷新Cookie，然后用新Cookie重试一次
// （每个路径最多重试一次，不循环）。通常包装 auth.LoginService.RefreshIfNeeded。
func (m *Manager) SetSessionRefresher(fn sessionRefresher) {
	m.refresh = fn
}

// tryRefresh 重新登录并刷新内部持有的Cookie；成功返回 true。
// 刷新成功后同步更新 API 客户端与浏览器爬虫的Cookie。
func (m *Manager) tryRefresh(ctx context.Context) bool {
	if m.refresh == nil {
		return false
	}
	newCookies, err := m.refresh(ctx)
	if err != nil || newCookies == nil {
		log.Printf("[抓取] 重新登录刷新Cookie失败: %v", err)
		return false
	}
	m.cookies = newCookies
	if m.apiClient != nil {
		m.apiClient.setCookies(newCookies.Cookies)
	}
	m.browserS.setCookies(newCookies.Cookies)
	log.Printf("[抓取] Cookie已刷新（%d个）", len(newCookies.Cookies))
	return true
}

// isSessionExpired 判断错误是否为会话失效（API 401/403 或页面跳转登录页）。
func (m *Manager) isSessionExpired(err error) bool {
	return errors.Is(err, errCookieExpired) || errors.Is(err, errLoginWall)
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
		return m.scrapeViaBrowser(ctx, game, tableIndex)
	case "auto":
		orders, err := m.scrapeViaAPI(ctx, game, tableIndex)
		if err == nil && len(orders) > 0 {
			return orders, nil
		}
		reason := failReason(err)

		// 会话失效（API 401/403）时：先重新登录刷新Cookie，再用新Cookie
		// 重试API一次，避免携带过期Cookie直接落入浏览器模式。只重试一次。
		if m.isSessionExpired(err) && m.tryRefresh(ctx) {
			log.Printf("[抓取] API会话失效，已重新登录，重试API %s 表格%d", game.Name, tableIndex)
			orders, err = m.scrapeViaAPI(ctx, game, tableIndex)
			if err == nil && len(orders) > 0 {
				return orders, nil
			}
			reason = failReason(err)
		}

		log.Printf("[抓取] API获取 %s 表格%d 失败: %s, 切换到浏览器模式", game.Name, tableIndex, reason)
		return m.scrapeViaBrowser(ctx, game, tableIndex)
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

// scrapeViaBrowser 通过浏览器模式抓取单个表格。
//
// 页面命中登录墙（Cookie失效的典型表现）时，重新登录刷新Cookie后
// 重试一次（只重试一次，不循环）。
func (m *Manager) scrapeViaBrowser(ctx context.Context, game config.GameConfig, tableIndex int) ([]models.RecycleOrder, error) {
	orders, err := m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
	if err == nil && len(orders) > 0 {
		return orders, nil
	}
	if errors.Is(err, errLoginWall) && m.tryRefresh(ctx) {
		log.Printf("[抓取] 浏览器模式命中登录墙，会话已刷新，重试 %s 表格%d", game.Name, tableIndex)
		return m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
	}
	return orders, err
}

// failReason 将抓取失败转为日志原因描述。
func failReason(err error) string {
	if err != nil {
		return err.Error()
	}
	return "返回空数据"
}
