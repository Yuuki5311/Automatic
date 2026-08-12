package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// errCookieExpired 会话已失效（API 返回 401/403）。
//
// Manager 依据该哨兵错误判断是否需要重新登录刷新Cookie后重试，
// 因此 API 客户端返回会话失效错误时必须经 %w 包裹本错误。
var errCookieExpired = errors.New("Cookie已过期")

// apiClient 通过反向工程出的内部API直接获取数据。
// API模式优先，失败时由 Manager 切换到浏览器兜底模式。
type apiClient struct {
	baseURL    string
	httpClient *http.Client
	cookies    []models.CookieEntry
}

func newAPIClient(baseURL string, cookies []models.CookieEntry) *apiClient {
	return &apiClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cookies:    cookies,
	}
}

// setCookies 更新客户端持有的Cookie（重新登录后由 Manager 调用）。
func (c *apiClient) setCookies(cookies []models.CookieEntry) {
	c.cookies = cookies
}

// ordersEndpoint 返回回收订单API端点（FetchRecycleOrders 与 ProbeAPI 共用）。
//
// API端点需先在浏览器DevTools中抓包确认，以下为推测的API路径模式：
// GET /api/v1/merchant/recycle/orders?game=xxx&page=1&pageSize=500
func (c *apiClient) ordersEndpoint() string {
	return c.baseURL + "/api/v1/merchant/recycle/orders"
}

// FetchRecycleOrders 通过API获取回收订单列表。
//
// API端点需要先在浏览器DevTools中抓包确认，以下为推测的API路径模式：
// GET /api/v1/merchant/recycle/orders?game=xxx&page=1&pageSize=500
//
// 响应结构兼容两种形态（见 parseJSONResponse）：
//   - {code, message, data: {list: [...]}}
//   - {code, message, data: [...]}
func (c *apiClient) FetchRecycleOrders(ctx context.Context, gameName string, tableIndex int) ([]models.RecycleOrder, error) {
	apiURL := c.ordersEndpoint()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 添加查询参数
	q := req.URL.Query()
	q.Add("game", gameName)
	q.Add("table", fmt.Sprintf("%d", tableIndex))
	q.Add("page", "1")
	q.Add("pageSize", "500") // 一次获取尽量多的数据
	req.URL.RawQuery = q.Encode()

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", c.baseURL+"/workbench")

	// 设置Cookie
	for _, cookie := range c.cookies {
		req.AddCookie(&http.Cookie{
			Name:   cookie.Name,
			Value:  cookie.Value,
			Domain: cookie.Domain,
			Path:   cookie.Path,
		})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w (HTTP %d)", errCookieExpired, resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API返回错误 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取API响应失败: %w", err)
	}

	// 统一走宽松解析（容忍站点返回的非RFC3339时间格式）
	return parseJSONResponse(body, gameName)
}

// ProbeAPI 探测实际订单接口是否可用。
//
// 对实际回收订单端点发起轻量 GET（page=1&pageSize=1）而非 HEAD /api/：
//   - 只在实际订单端点返回 200 时视为可用，避免端点路径配置错误时
//     404 等非5xx状态码被误判为"接口可用"（旧实现的假阳性）；
//   - 401/403 视为会话失效，返回包裹 errCookieExpired 的错误，
//     由 Manager 触发重新登录后用新Cookie重试。
//
// 若真实端点路径与 FetchRecycleOrders 不一致（尚未抓包确认），
// 本探测只是连通性检查，需以 ordersEndpoint 的实际路径为准。
func (c *apiClient) ProbeAPI(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ordersEndpoint(), nil)
	if err != nil {
		return fmt.Errorf("创建探测请求失败: %w", err)
	}
	q := req.URL.Query()
	q.Add("page", "1")
	q.Add("pageSize", "1") // 轻量探测：仅验证端点路径与会话有效性
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	for _, cookie := range c.cookies {
		req.AddCookie(&http.Cookie{
			Name:   cookie.Name,
			Value:  cookie.Value,
			Domain: cookie.Domain,
			Path:   cookie.Path,
		})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("API探测失败: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d)", errCookieExpired, resp.StatusCode)
	default:
		return fmt.Errorf("API探测返回错误 (HTTP %d)", resp.StatusCode)
	}
}
