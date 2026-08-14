// Package leyoo 对接 open.leyoo888.com 店铺 Cookie 列表接口。
package leyoo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultListURL = "https://open.leyoo888.com/open/ck_account/list"

// RemoteAccount 接口返回的一条店铺记录（敏感字段仅内存使用）。
type RemoteAccount struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Remark        string `json:"remark"`
	Cookie        string `json:"cookie"`
	Mobile        string `json:"mobile"`
	Platform      string `json:"platform"`
	PlatformKey   string `json:"platform_key"`
	SupplierID    int    `json:"supplier_id"`
	ThirdAccount  string `json:"third_account"`
	ThirdPassword string `json:"third_password"`
}

type listResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data []RemoteAccount `json:"data"`
}

// Client 拉取店铺 Cookie 列表。
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultListURL
	}
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ListCatBySupplier 拉取列表并过滤 supplier_id + platform_key=cat。
// 服务端可能忽略 supplier_id 查询参数，故在本地再筛一遍。
func (c *Client) ListCatBySupplier(supplierID int) ([]RemoteAccount, error) {
	if c == nil {
		c = NewClient("")
	}
	if supplierID <= 0 {
		supplierID = 1
	}
	req, err := http.NewRequest(http.MethodGet, c.BaseURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求店铺列表失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("店铺列表 HTTP %d", resp.StatusCode)
	}
	var parsed listResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析店铺列表失败: %w", err)
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("店铺列表错误: code=%d msg=%s", parsed.Code, parsed.Msg)
	}
	out := make([]RemoteAccount, 0, len(parsed.Data))
	for _, a := range parsed.Data {
		if a.SupplierID != supplierID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(a.PlatformKey), "cat") {
			continue
		}
		if strings.TrimSpace(a.Mobile) == "" {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}
