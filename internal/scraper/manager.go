package scraper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

type boardFetcher interface {
	FetchBoardStats(ctx context.Context, gameName string) (models.GameBoardStats, error)
}

var errLoginWall = errors.New("页面跳转到登录页，会话已失效")

type sessionRefresher func(ctx context.Context) (*models.CookieData, error)

// resultReporter 单表抓取结果回调（由调用方注入，用于更新状态看板）。
type resultReporter func(tableKey string, count int, err error)

type Manager struct {
	cfg       *config.Config
	cookies   *models.CookieData
	apiClient *apiClient
	fetcher   boardFetcher
	browserS  *browserScraper
	refresh   sessionRefresher
	report    resultReporter
}

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
		m.fetcher = m.apiClient
		if path := cfg.JYM.CookiePath; path != "" && cookies != nil {
			m.apiClient.persistCookies = func(entries []models.CookieEntry) error {
				cookies.Cookies = entries
				return auth.SaveCookies(path, cookies)
			}
		}
	}
	m.browserS = newBrowserScraper(cfg, browserMgr, cookieEntries)
	return m
}

func (m *Manager) SetSessionRefresher(fn sessionRefresher) {
	m.refresh = fn
}

// SetResultReporter 注册单表抓取结果回调，每完成一个表格的抓取即调用。
func (m *Manager) SetResultReporter(fn resultReporter) {
	m.report = fn
}

func (m *Manager) tryRefresh(ctx context.Context) bool {
	if m.refresh == nil {
		return false
	}
	newCookies, err := m.refresh(ctx)
	if err != nil || newCookies == nil {
		slog.Error("重新登录刷新Cookie失败", "component", "scraper", "error", err)
		return false
	}
	m.cookies = newCookies
	if m.apiClient != nil {
		m.apiClient.setCookies(newCookies.Cookies)
	}
	m.browserS.setCookies(newCookies.Cookies)
	slog.Info("Cookie已刷新", "component", "scraper", "cookie_count", len(newCookies.Cookies))
	return true
}

func (m *Manager) isSessionExpired(err error) bool {
	return errors.Is(err, errCookieExpired) || errors.Is(err, errLoginWall)
}

func (m *Manager) board() boardFetcher {
	if m.fetcher != nil {
		return m.fetcher
	}
	return m.apiClient
}

// YesterdayDate 返回 now 所在本地时区的昨日日期（YYYY-MM-DD）。
func YesterdayDate(now time.Time) string {
	return now.In(time.Local).AddDate(0, 0, -1).Format("2006-01-02")
}

// MonthStartDate 返回 now 所在本地时区当月 1 日（YYYY-MM-DD）。
func MonthStartDate(now time.Time) string {
	t := now.In(time.Local)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local).Format("2006-01-02")
}

// ScrapeDayDate 返回执行抓取当日的本地日期（YYYY-MM-DD），写入快照/飞书「日期」。
func ScrapeDayDate(now time.Time) string {
	return now.In(time.Local).Format("2006-01-02")
}

func (m *Manager) ScrapeBoardAll(ctx context.Context) (models.BoardStatsSnapshot, error) {
	if m.cfg == nil {
		return models.BoardStatsSnapshot{}, fmt.Errorf("配置为空，无法抓取")
	}
	snap := models.BoardStatsSnapshot{Date: ScrapeDayDate(time.Now()), ScrapedAt: time.Now()}
	fetcher := m.board()
	if fetcher == nil {
		return snap, fmt.Errorf("API客户端未初始化（配置缺失）")
	}

	var hardFails []string
	for _, game := range m.cfg.Scraper.Games {
		gs, err := fetcher.FetchBoardStats(ctx, game.Name)
		if err != nil && m.isSessionExpired(err) && m.tryRefresh(ctx) {
			slog.Info("看板会话失效，已重新登录，重试", "component", "scraper", "game", game.Name)
			gs, err = fetcher.FetchBoardStats(ctx, game.Name)
		}
		if err != nil {
			if m.isSessionExpired(err) {
				slog.Warn("看板会话失效且刷新后仍失败", "component", "scraper", "game", game.Name, "error", err)
				return snap, fmt.Errorf("登录失败: %w", err)
			}
			if IsConfirmedGameQueryFail(err) {
				slog.Warn("已开通游戏但查询失败", "component", "scraper", "game", game.Name, "error", err)
				hardFails = append(hardFails, game.Name)
				if m.report != nil {
					m.report(game.Name, 0, err)
				}
				continue
			}
			// 无接口 / 无数据 / 其它未确认开通的查不到 → 跳过，不记失败
			slog.Info("跳过游戏（无接口或未确认开通）", "component", "scraper", "game", game.Name, "error", err)
			if m.report != nil {
				m.report(game.Name, 0, nil)
			}
			continue
		}
		if len(gs.Metrics) == 0 {
			slog.Info("看板无指标数据，跳过", "component", "scraper", "game", game.Name)
			if m.report != nil {
				m.report(game.Name, 0, nil)
			}
			continue
		}
		snap.Games = append(snap.Games, gs)
		if m.report != nil {
			m.report(game.Name, len(gs.Metrics), nil)
		}
	}
	if len(hardFails) > 0 {
		return snap, &HardGameScrapeError{Games: hardFails}
	}
	return snap, nil
}

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
				slog.Warn("表格抓取失败", "component", "scraper", "game", game.Name, "table", i, "error", err)
				if m.report != nil {
					m.report(tableKey, 0, err)
				}
				continue
			}
			result[tableKey] = orders
			slog.Info("抓取完成", "component", "scraper", "table", tableKey, "count", len(orders))
			if m.report != nil {
				m.report(tableKey, len(orders), nil)
			}
		}
	}

	return result, nil
}

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
		return m.scrapeViaAPI(ctx, game, tableIndex)
	case "browser":
		return m.scrapeViaBrowser(ctx, game, tableIndex)
	case "auto":
		orders, err := m.scrapeViaAPI(ctx, game, tableIndex)
		if err == nil && len(orders) > 0 {
			return orders, nil
		}
		reason := failReason(err)

		if m.isSessionExpired(err) && m.tryRefresh(ctx) {
			slog.Info("API会话失效，已重新登录，重试API", "component", "scraper", "game", game.Name, "table", tableIndex)
			orders, err = m.scrapeViaAPI(ctx, game, tableIndex)
			if err == nil && len(orders) > 0 {
				return orders, nil
			}
			reason = failReason(err)
		}

		slog.Warn("API获取失败，切换到浏览器模式", "component", "scraper", "game", game.Name, "table", tableIndex, "reason", reason)
		return m.scrapeViaBrowser(ctx, game, tableIndex)
	default:
		return nil, fmt.Errorf("未知抓取模式: %s", mode)
	}
}

func (m *Manager) scrapeViaAPI(ctx context.Context, game config.GameConfig, tableIndex int) ([]models.RecycleOrder, error) {
	if m.apiClient == nil {
		return nil, fmt.Errorf("API客户端未初始化（配置缺失）")
	}
	if err := m.apiClient.ProbeAPI(ctx); err != nil {
		return nil, fmt.Errorf("API探测失败: %w", err)
	}
	return m.apiClient.FetchRecycleOrders(ctx, game.Name, tableIndex)
}

func (m *Manager) scrapeViaBrowser(ctx context.Context, game config.GameConfig, tableIndex int) ([]models.RecycleOrder, error) {
	orders, err := m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
	if err == nil && len(orders) > 0 {
		return orders, nil
	}
	if errors.Is(err, errLoginWall) && m.tryRefresh(ctx) {
		slog.Info("浏览器模式命中登录墙，会话已刷新，重试", "component", "scraper", "game", game.Name, "table", tableIndex)
		return m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
	}
	return orders, err
}

func failReason(err error) string {
	if err != nil {
		return err.Error()
	}
	return "返回空数据"
}
