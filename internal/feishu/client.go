// Package feishu 提供飞书 Open API 客户端，用于读写多维表格（Bitable）。
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

const feishuBaseURL = "https://open.feishu.cn/open-apis"

// 飞书 Open API 业务错误码
const (
	errCodeTokenInvalid = 99991663 // tenant_access_token 无效或已过期
	errCodeThrottled    = 99991400 // 请求被限流（QPS 超限）
)

// 客户端默认参数
const (
	defaultHTTPTimeout = 30 * time.Second
	defaultMinInterval = 200 * time.Millisecond // 两次 API 请求的最小间隔（约 5 QPS，保守避免限流）
	defaultMaxRetries  = 3
	defaultBackoffBase = 200 * time.Millisecond
	tokenSafetyMargin  = 5 * time.Minute // token 提前过期时间，避免边缘失效
)

// APIError 飞书 Open API 返回的业务错误
type APIError struct {
	Code       int
	Msg        string
	HTTPStatus int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("飞书API错误 (code=%d): %s, body=%s", e.Code, e.Msg, e.Body)
}

// Client 飞书 Open API 客户端。
// 内部包含 tenant_access_token 缓存（双重检查锁）、请求限流与重试。
type Client struct {
	appID      string
	appSecret  string
	baseURL    string
	bitableID  string
	httpClient *http.Client

	// 限流参数（默认 5 QPS；测试中可调小或置 0 关闭）
	minInterval time.Duration
	limitMu     sync.Mutex
	lastRequest time.Time

	// 重试参数（测试中可调小）
	maxRetries  int
	backoffBase time.Duration

	// tenant_access_token 缓存
	mu       sync.RWMutex
	token    string
	tokenExp time.Time
}

// NewClient 创建飞书客户端
func NewClient(cfg *config.FeishuConfig) *Client {
	return &Client{
		appID:       cfg.AppID,
		appSecret:   cfg.AppSecret,
		baseURL:     feishuBaseURL,
		bitableID:   cfg.BitableID,
		httpClient:  &http.Client{Timeout: defaultHTTPTimeout},
		minInterval: defaultMinInterval,
		maxRetries:  defaultMaxRetries,
		backoffBase: defaultBackoffBase,
	}
}

// GetTenantAccessToken 获取并缓存 tenant_access_token（双重检查锁，提前5分钟过期）
func (c *Client) GetTenantAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	cached := c.token
	valid := cached != "" && time.Now().Before(c.tokenExp)
	c.mu.RUnlock()
	if valid {
		return cached, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 双重检查：可能已有其他 goroutine 完成刷新
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}

	body, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取飞书token失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // 秒
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析飞书token响应失败: %w", err)
	}
	if result.Code != 0 {
		return "", &APIError{Code: result.Code, Msg: result.Msg, HTTPStatus: resp.StatusCode}
	}

	expire := time.Duration(result.Expire) * time.Second
	if expire > tokenSafetyMargin {
		expire -= tokenSafetyMargin
	} else {
		expire = time.Minute // 兜底：异常短的有效期
	}
	c.token = result.TenantAccessToken
	c.tokenExp = time.Now().Add(expire)

	return c.token, nil
}

// ListRecords 列出表格全部记录（分页拉取，用于去重）。
// 便捷方法，使用配置中的 BitableID。
func (c *Client) ListRecords(ctx context.Context, tableID string) ([]models.FeishuRecord, error) {
	return NewBitableOps(c, c.bitableID).listAllRecords(ctx, tableID)
}

// BatchInsertRecords 批量写入/更新回收订单记录（按 OrderID 去重）。
// 便捷方法，使用配置中的 BitableID。
func (c *Client) BatchInsertRecords(ctx context.Context, tableID string, records []models.RecycleOrder) error {
	return NewBitableOps(c, c.bitableID).BatchInsertOrders(ctx, tableID, records)
}

// doRequest 执行带认证的 API 请求，内置限流与重试。
// 重试策略：网络错误、HTTP 5xx、限流(99991400)、token失效(99991663，强制刷新后重试)。
// 注意：POST 类请求在网络失败后重试可能造成服务端重复处理，上层按 OrderID 去重兜底。
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		c.rateLimit()
		lastErr = c.doRequestOnce(ctx, method, path, body, result)
		if lastErr == nil {
			return nil
		}
		if !c.retryable(lastErr) {
			return lastErr
		}
		if !c.sleep(ctx, time.Duration(1<<uint(attempt))*c.backoffBase) {
			return lastErr // 上下文已取消，放弃重试
		}
	}
	return lastErr
}

// retryable 判断错误是否值得重试；token 失效时清除缓存以便重新获取
func (c *Client) retryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case errCodeTokenInvalid:
			c.mu.Lock()
			c.token = ""
			c.tokenExp = time.Time{}
			c.mu.Unlock()
			return true
		case errCodeThrottled:
			return true
		default:
			return apiErr.HTTPStatus >= http.StatusInternalServerError
		}
	}
	// 网络层错误（连接失败、超时等）
	return true
}

// doRequestOnce 单次执行带认证的 API 请求
func (c *Client) doRequestOnce(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	token, err := c.GetTenantAccessToken(ctx)
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var apiResp struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &apiResp); err != nil {
		return fmt.Errorf("解析飞书响应失败: %w, body=%s", err, string(rawBody))
	}
	if apiResp.Code != 0 {
		return &APIError{
			Code:       apiResp.Code,
			Msg:        apiResp.Msg,
			HTTPStatus: resp.StatusCode,
			Body:       string(rawBody),
		}
	}

	if result != nil {
		return json.Unmarshal(rawBody, result)
	}
	return nil
}

// rateLimit 全局最小请求间隔限流（并发请求按到达顺序排队）
func (c *Client) rateLimit() {
	c.limitMu.Lock()
	defer c.limitMu.Unlock()

	if c.minInterval <= 0 {
		return
	}
	if wait := c.minInterval - time.Since(c.lastRequest); wait > 0 {
		time.Sleep(wait)
	}
	c.lastRequest = time.Now()
}

// sleep 可被 context 取消的等待
func (c *Client) sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
