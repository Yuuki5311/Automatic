package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

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

// FetchRecycleOrders 通过API获取回收订单列表。
//
// API端点需要先在浏览器DevTools中抓包确认，以下为推测的API路径模式：
// GET /api/v1/merchant/recycle/orders?game=xxx&page=1&pageSize=500
//
// 响应结构兼容两种形态（见 parseJSONResponse）：
//   - {code, message, data: {list: [...]}}
//   - {code, message, data: [...]}
func (c *apiClient) FetchRecycleOrders(ctx context.Context, gameName string, tableIndex int) ([]models.RecycleOrder, error) {
	// API URL需根据实际抓包结果替换
	// 以下为推测的API路径模式
	apiURL := fmt.Sprintf("%s/api/v1/merchant/recycle/orders", c.baseURL)

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

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("Cookie已过期 (HTTP %d)", resp.StatusCode)
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

// ProbeAPI 探测API是否可用（发送轻量请求测试连通性）。
func (c *apiClient) ProbeAPI(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, "HEAD", c.baseURL+"/api/", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}
