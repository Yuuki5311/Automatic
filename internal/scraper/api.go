package scraper

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

var errCookieExpired = errors.New("Cookie已过期")

// errNoPrivilege 店铺无该游戏看板权限（没有对应接口）。
var errNoPrivilege = errors.New("店铺无此游戏看板权限")

// errNoBoardData 接口已通但当日无指标（不算抓取失败）。
var errNoBoardData = errors.New("无看板指标数据")

// errConfirmedGameQuery 已确认店铺开通该游戏看板，但指标查询/解析失败（记为抓取失败）。
var errConfirmedGameQuery = errors.New("已开通游戏但查询失败")

// IsNoPrivilege 判断是否为「店铺无此游戏接口」类错误（登录成功但不应记为抓取失败）。
func IsNoPrivilege(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errNoPrivilege) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NO_PRIVILEGE") || strings.Contains(msg, "FAIL_BIZ_NO_PRIVILEGE")
}

// IsConfirmedGameQueryFail 已确认开通该游戏，但无法查出指标。
func IsConfirmedGameQueryFail(err error) bool {
	return err != nil && errors.Is(err, errConfirmedGameQuery)
}

// HardGameScrapeError 表示登录有效，且已确认开通的游戏查询失败。
// 调用方可用 errors.As 取出游戏名；若 Snapshot 中已有成功游戏，仍应落盘/同步。
type HardGameScrapeError struct {
	Games []string
}

func (e *HardGameScrapeError) Error() string {
	if e == nil || len(e.Games) == 0 {
		return "抓取游戏失败"
	}
	return "抓取" + strings.Join(e.Games, "、") + "失败"
}

var mtopBaseURL = "https://mtop.jiaoyimao.com"

const (
	mtopAppKey = "12574478"

	// 各游戏ID（从浏览器 Network 抓包获取）
	gameIDNaruto    = 1003132 // 火影忍者
	gameIDGenshin   = 1009609 // 原神
	gameIDZZZ       = 1013597 // 绝区零
	gameIDHSR       = 2000334 // 崩坏：星穹铁道
	gameIDWuthering = 2007615 // 鸣潮
	gameIDDelta     = 2007840 // 三角洲行动
	gameIDPeace     = 1006473 // 和平精英
	gameIDWangzhe   = 1002416 // 王者荣耀
)

// gameNameToID 游戏名 → MTOP gameId 映射
var gameNameToID = map[string]int{
	"火影忍者":    gameIDNaruto,
	"原神":      gameIDGenshin,
	"绝区零":     gameIDZZZ,
	"崩坏：星穹铁道": gameIDHSR,
	"鸣潮":      gameIDWuthering,
	"三角洲行动":   gameIDDelta,
	"和平精英":    gameIDPeace,
	"王者荣耀":    gameIDWangzhe,
}

// BoardGameNames 返回看板抓取支持的游戏名（稳定顺序）。
func BoardGameNames() []string {
	return []string{
		"火影忍者", "原神", "绝区零", "崩坏：星穹铁道", "鸣潮", "三角洲行动", "和平精英", "王者荣耀",
	}
}

type apiClient struct {
	httpClient     *http.Client
	cookies        []models.CookieEntry
	mtopToken      string // _m_h5_tk 中提取的 token 部分
	persistCookies func(cookies []models.CookieEntry) error
}

func newAPIClient(baseURL string, cookies []models.CookieEntry) *apiClient {
	c := &apiClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cookies:    cookies,
	}
	c.extractToken()
	return c
}

func (c *apiClient) setCookies(cookies []models.CookieEntry) {
	c.cookies = cookies
	c.extractToken()
}

// extractToken 从 _m_h5_tk cookie 中提取 token（格式: token_timestamp）
func (c *apiClient) extractToken() {
	for _, ck := range c.cookies {
		if ck.Name == "_m_h5_tk" {
			if idx := strings.Index(ck.Value, "_"); idx > 0 {
				c.mtopToken = ck.Value[:idx]
			} else {
				c.mtopToken = ck.Value
			}
			return
		}
	}
}

// mtopSign 计算 MTOP 签名: md5(token + "&" + timestamp + "&" + appKey + "&" + data)
func mtopSign(token string, timestamp int64, data string) string {
	raw := fmt.Sprintf("%s&%d&%s&%s", token, timestamp, mtopAppKey, data)
	return fmt.Sprintf("%x", md5.Sum([]byte(raw)))
}

// getCookieHeader 构造 Cookie 请求头
func (c *apiClient) getCookieHeader() string {
	var parts []string
	for _, ck := range c.cookies {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

// FetchRecycleOrders 通过 MTOP API 获取回收订单列表。
//
// Deprecated: 订单列表 API 尚未确认；请使用 FetchBoardStats 获取看板指标。
func (c *apiClient) FetchRecycleOrders(ctx context.Context, gameName string, tableIndex int) ([]models.RecycleOrder, error) {
	if _, err := c.FetchBoardStats(ctx, gameName); err != nil {
		return nil, err
	}
	return nil, nil
}

// BoardStatsTimeKey 看板统计时间维度：本月（月初至今），与商户后台「本月」一致。
const BoardStatsTimeKey = "month"

// FetchBoardStats 通过 MTOP recyclestats API 获取指定游戏的本月（月初至今）看板指标。
func (c *apiClient) FetchBoardStats(ctx context.Context, gameName string) (models.GameBoardStats, error) {
	gameID, ok := gameNameToID[gameName]
	if !ok {
		return models.GameBoardStats{}, fmt.Errorf("未找到游戏ID: %s", gameName)
	}
	return c.fetchBoardStats(ctx, gameName, gameID, true)
}

func (c *apiClient) fetchBoardStats(ctx context.Context, gameName string, gameID int, retryToken bool) (models.GameBoardStats, error) {
	statsURL, err := c.buildRecycleStatsURL(gameID)
	if err != nil {
		return models.GameBoardStats{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statsURL, nil)
	if err != nil {
		return models.GameBoardStats{}, err
	}
	if hdr := c.getCookieHeader(); hdr != "" {
		req.Header.Set("Cookie", hdr)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return models.GameBoardStats{}, fmt.Errorf("API请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.GameBoardStats{}, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return models.GameBoardStats{}, fmt.Errorf("API返回 %d: %s", resp.StatusCode, string(body))
	}

	tokenRefreshed := c.applySetCookies(resp.Cookies())

	stats, err := ParseRecycleStatsJSON(gameName, gameID, body)
	if err != nil {
		if retryToken && tokenRefreshed && errors.Is(err, errCookieExpired) {
			slog.Info("MTOP token 已刷新，重试", "component", "scraper", "game", gameName)
			return c.fetchBoardStats(ctx, gameName, gameID, false)
		}
		return models.GameBoardStats{}, err
	}
	stats.FetchedAt = time.Now()
	return stats, nil
}

func (c *apiClient) applySetCookies(setCookies []*http.Cookie) (tokenRefreshed bool) {
	updated := false
	for _, sc := range setCookies {
		if sc == nil || sc.Name == "" || sc.Value == "" {
			continue
		}
		if sc.Name == "_m_h5_tk" {
			tokenRefreshed = true
		}
		found := false
		for i, ck := range c.cookies {
			if ck.Name == sc.Name {
				c.cookies[i].Value = sc.Value
				found = true
				updated = true
				break
			}
		}
		if !found {
			c.cookies = append(c.cookies, models.CookieEntry{
				Name:   sc.Name,
				Value:  sc.Value,
				Domain: sc.Domain,
				Path:   sc.Path,
			})
			updated = true
		}
	}
	if updated {
		c.extractToken()
	}
	if tokenRefreshed && c.persistCookies != nil {
		if err := c.persistCookies(c.cookies); err != nil {
			slog.Warn("保存刷新后的 _m_h5_tk 失败", "component", "scraper", "error", err)
		}
	}
	return tokenRefreshed
}

// ParseRecycleStatsJSON 解析 MTOP recyclestats 响应体（纯函数，便于单测）。
func ParseRecycleStatsJSON(gameName string, gameID int, body []byte) (models.GameBoardStats, error) {
	var mtopResp struct {
		Ret  []string        `json:"ret"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &mtopResp); err != nil {
		return models.GameBoardStats{}, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(mtopResp.Ret) == 0 || !strings.HasPrefix(mtopResp.Ret[0], "SUCCESS") {
		msg := "未知错误"
		if len(mtopResp.Ret) > 0 {
			msg = mtopResp.Ret[0]
		}
		if strings.Contains(msg, "SESSION") || strings.Contains(msg, "TOKEN") {
			return models.GameBoardStats{}, fmt.Errorf("%w: %s", errCookieExpired, msg)
		}
		if strings.Contains(msg, "NO_PRIVILEGE") {
			return models.GameBoardStats{}, fmt.Errorf("%w: %s", errNoPrivilege, msg)
		}
		return models.GameBoardStats{}, fmt.Errorf("API错误: %s", msg)
	}

	var data struct {
		Result []struct {
			Title      string `json:"title"`
			StaData    string `json:"staData"`
			StaUnit    string `json:"staUnit"`
			Properties struct {
				Tips string `json:"tips"`
			} `json:"properties"`
		} `json:"result"`
	}
	if err := json.Unmarshal(mtopResp.Data, &data); err != nil {
		return models.GameBoardStats{}, fmt.Errorf("%w: 解析 data 失败: %v", errConfirmedGameQuery, err)
	}

	metrics := make([]models.BoardMetric, 0, len(data.Result))
	for _, item := range data.Result {
		metrics = append(metrics, models.BoardMetric{
			Title: item.Title,
			Value: item.StaData,
			Unit:  item.StaUnit,
			Tips:  item.Properties.Tips,
		})
	}
	if len(metrics) == 0 {
		// SUCCESS 但无指标：接口可达且当日无数据，不算「有游戏却查不到」
		return models.GameBoardStats{}, fmt.Errorf("%w", errNoBoardData)
	}

	return models.GameBoardStats{
		GameName: gameName,
		GameID:   gameID,
		TimeKey:  BoardStatsTimeKey,
		Metrics:  metrics,
		RawJSON:  string(mtopResp.Data),
	}, nil
}

// buildRecycleStatsURL 构建回收统计API URL（带MTOP签名）。
func (c *apiClient) buildRecycleStatsURL(gameID int) (string, error) {
	apiName := "mtop.com.jym.merchant.board.recyclestats"
	version := "1.0"
	data := fmt.Sprintf(`{"gameId":%d,"time":%q}`, gameID, BoardStatsTimeKey)
	ts := time.Now().UnixMilli()

	sign := mtopSign(c.mtopToken, ts, data)

	u, _ := url.Parse(mtopBaseURL)
	u.Path = fmt.Sprintf("/h5/%s/%s/", apiName, version)
	q := u.Query()
	q.Set("jsv", "2.6.2")
	q.Set("appKey", mtopAppKey)
	q.Set("t", strconv.FormatInt(ts, 10))
	q.Set("sign", sign)
	q.Set("dataType", "json")
	q.Set("valueType", "original")
	q.Set("api", apiName)
	q.Set("v", version)
	q.Set("type", "originaljson")
	q.Set("preventFallback", "true")
	q.Set("data", data)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ProbeAPI 探测 MTOP API 是否可用。
func (c *apiClient) ProbeAPI(ctx context.Context) error {
	if c.mtopToken == "" {
		return fmt.Errorf("MTOP token 为空（_m_h5_tk cookie缺失）")
	}
	// 用火影忍者的 gameId 做轻量探测
	u, err := c.buildRecycleStatsURL(gameIDNaruto)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	for _, ck := range c.cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value, Domain: ck.Domain, Path: ck.Path})
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("API探测失败: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("API探测返回错误 (HTTP %d)", resp.StatusCode)
	}
	return nil
}
