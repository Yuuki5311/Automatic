package feishu

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

const (
	testBitableID = "bascn_test_bitable"
	testTableID   = "tbl_test_table"
)

// counter 并发安全的计数器
type counter struct {
	atomic.Int64
}

func (c *counter) Add()       { c.Int64.Add(1) }
func (c *counter) Get() int64 { return c.Int64.Load() }

// newTestClient 创建指向 mock 服务器的客户端：关闭限流、加速重试
func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c := NewClient(&config.FeishuConfig{
		AppID:     "cli_test_app",
		AppSecret: "test_secret",
		BitableID: testBitableID,
	})
	c.baseURL = baseURL
	c.minInterval = 0
	c.backoffBase = time.Millisecond
	c.maxRetries = 3
	return c
}

// tokenHandler mock 的 tenant_access_token 接口，每次调用返回带序号的 token（t1, t2, ...）
func tokenHandler(calls *counter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w,
			`{"code":0,"msg":"success","tenant_access_token":"t%d","expire":7200}`,
			calls.Get())
	}
}
