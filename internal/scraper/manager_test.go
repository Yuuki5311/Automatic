package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// testAPIServer 模拟交易猫内部API。
// 记录收到的请求参数，便于断言API模式确实按预期调用。
type testAPIServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []testAPIRequest
}

type testAPIRequest struct {
	path    string
	method  string
	query   map[string][]string
	cookies []*http.Cookie
}

func newTestAPIServer(handler func(w http.ResponseWriter, r *http.Request)) *testAPIServer {
	s := &testAPIServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, testAPIRequest{
			path:    r.URL.Path,
			method:  r.Method,
			query:   r.URL.Query(),
			cookies: r.Cookies(),
		})
		s.mu.Unlock()
		handler(w, r)
	}))
	return s
}

func (s *testAPIServer) Close() {
	s.srv.Close()
}

func (s *testAPIServer) hasRequest(method, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if r.method == method && r.path == path {
			return true
		}
	}
	return false
}

// writeOrdersResponse 输出带非RFC3339时间格式的订单列表，验证宽松解析。
func writeOrdersResponse(w http.ResponseWriter, orders []map[string]interface{}) {
	payload := map[string]interface{}{
		"code":    0,
		"message": "ok",
		"data": map[string]interface{}{
			"total": len(orders),
			"list":  orders,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// newTestManager 构造测试用 Manager（浏览器管理器传 nil，
// 浏览器模式调用会返回明确错误，便于断言兜底路由）。
func newTestManager(cfg *config.Config, cookies *models.CookieData) *Manager {
	return NewManager(cfg, nil, cookies)
}

func sampleOrderJSON(game string) map[string]interface{} {
	return map[string]interface{}{
		"order_id":      "NO-" + game,
		"game_name":     game,
		"server_region": "官服",
		"account_info":  "账号" + game,
		"price":         199.5,
		"status":        "回收中",
		"create_time":   "2026-08-12 10:30:00",
	}
}

func TestScrapeGameTable_APIModeSuccess(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeOrdersResponse(w, []map[string]interface{}{sampleOrderJSON("原神")})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM: config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{
			Mode:  "api",
			Games: []config.GameConfig{{Name: "原神", TableCount: 1}},
		},
	}
	cookies := &models.CookieData{Cookies: []models.CookieEntry{
		{Name: "token", Value: "secret", Domain: ".jiaoyimao.com", Path: "/"},
	}}
	m := newTestManager(cfg, cookies)

	orders, err := m.ScrapeGameTable(context.Background(), cfg.Scraper.Games[0], 0)
	if err != nil {
		t.Fatalf("ScrapeGameTable(api mode) failed: %v", err)
	}
	if len(orders) != 1 || orders[0].OrderID != "NO-原神" {
		t.Fatalf("unexpected orders: %+v", orders)
	}
	// 时间字段应被宽松解析
	if !orders[0].CreateTime.Equal(time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("CreateTime = %v, want 2026-08-12 10:30:00", orders[0].CreateTime)
	}

	// API调用参数断言：game/page/pageSize/table + Cookie 透传
	var got *testAPIRequest
	for _, r := range srv.requests {
		if r.method == http.MethodGet && r.path == "/api/v1/merchant/recycle/orders" {
			got = &r
			break
		}
	}
	if got == nil {
		t.Fatal("GET /api/v1/merchant/recycle/orders was never called")
	}
	if got.query["game"][0] != "原神" || got.query["page"][0] != "1" || got.query["pageSize"][0] != "500" {
		t.Errorf("unexpected query params: %+v", got.query)
	}
	tokenFound := false
	for _, c := range got.cookies {
		if c.Name == "token" && c.Value == "secret" {
			tokenFound = true
		}
	}
	if !tokenFound {
		t.Error("token cookie was not forwarded to API")
	}
}

func TestScrapeGameTable_APIModeExpiredCookie(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "api"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil {
		t.Fatal("expected error for expired cookie, got nil")
	}
	if !strings.Contains(err.Error(), "Cookie已过期") {
		t.Fatalf("error should mention expired cookie, got: %v", err)
	}
}

func TestScrapeGameTable_AutoModeAPIFailureFallsBackToBrowser(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	// browserMgr 为 nil → 浏览器兜底路径应返回明确错误，
	// 证明流程确实路由到了浏览器模式而不是静默返回空数据。
	m := newTestManager(cfg, &models.CookieData{})

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil {
		t.Fatal("expected error from browser fallback path, got nil")
	}
	if !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("error should come from browser fallback, got: %v", err)
	}
}

func TestScrapeGameTable_AutoModeAPISuccessNoBrowser(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeOrdersResponse(w, []map[string]interface{}{sampleOrderJSON("原神")})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	orders, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err != nil {
		t.Fatalf("auto mode should succeed via API: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("got %d orders, want 1", len(orders))
	}
}

func TestScrapeGameTable_AutoModeEmptyListFallsBackToBrowser(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		// 空列表：API成功但无数据，也应切换到浏览器兜底
		writeOrdersResponse(w, nil)
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("empty API list should fall back to browser, got err: %v", err)
	}
}

func TestScrapeGameTable_AutoModeProbeFailureFallsBackToBrowser(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 探测与业务接口均503
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("probe failure should fall back to browser, got err: %v", err)
	}
	if !srv.hasRequest(http.MethodHead, "/api/") {
		t.Fatal("ProbeAPI HEAD request was not sent")
	}
}

func TestScrapeGameTable_BrowserOnlyMode(t *testing.T) {
	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: "http://example.invalid"},
		Scraper: config.ScraperConfig{Mode: "browser"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("browser-only mode without manager should error, got: %v", err)
	}
}

func TestScrapeGameTable_UnknownMode(t *testing.T) {
	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: "http://example.invalid"},
		Scraper: config.ScraperConfig{Mode: "weird-mode"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	if _, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0); err == nil {
		t.Fatal("unknown mode should return error")
	}
}

func TestScrapeGameTable_EmptyModeDefaultsToAuto(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeOrdersResponse(w, []map[string]interface{}{sampleOrderJSON("原神")})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: ""}, // 未配置时按 auto 处理
	}
	m := newTestManager(cfg, &models.CookieData{})

	if _, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0); err != nil {
		t.Fatalf("empty mode should default to auto and succeed: %v", err)
	}
}

func TestScrapeGame_MergesAllTables(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		game := r.URL.Query().Get("game")
		table := r.URL.Query().Get("table")
		order := sampleOrderJSON(game)
		order["order_id"] = game + "-table" + table
		writeOrdersResponse(w, []map[string]interface{}{order})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM: config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{
			Mode:  "api",
			Games: []config.GameConfig{{Name: "原神", TableCount: 2}},
		},
	}
	m := newTestManager(cfg, &models.CookieData{})

	orders, err := m.ScrapeGame(context.Background(), cfg.Scraper.Games[0])
	if err != nil {
		t.Fatalf("ScrapeGame failed: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("got %d orders, want 2 (one per table)", len(orders))
	}
	if orders[0].OrderID != "原神-table0" || orders[1].OrderID != "原神-table1" {
		t.Errorf("orders not merged in table order: %+v", orders)
	}
}

func TestScrapeGame_TableFailureReturnsError(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("table") == "1" {
			w.WriteHeader(http.StatusUnauthorized) // 第二个表格Cookie过期
			return
		}
		writeOrdersResponse(w, []map[string]interface{}{sampleOrderJSON("原神")})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "api"},
	}
	m := newTestManager(cfg, &models.CookieData{})

	if _, err := m.ScrapeGame(context.Background(), config.GameConfig{Name: "原神", TableCount: 2}); err == nil {
		t.Fatal("ScrapeGame should return error when a table fails")
	}
}

func TestScrapeAll_TableKeys(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		game := r.URL.Query().Get("game")
		writeOrdersResponse(w, []map[string]interface{}{sampleOrderJSON(game)})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM: config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{
			Mode: "api",
			Games: []config.GameConfig{
				{Name: "原神", TableCount: 2},
				{Name: "火影忍者", TableCount: 1},
			},
		},
	}
	m := newTestManager(cfg, &models.CookieData{})

	result, err := m.ScrapeAll(context.Background())
	if err != nil {
		t.Fatalf("ScrapeAll failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("got %d table keys, want 3: %+v", len(result), result)
	}
	for _, key := range []string{"原神_table1", "原神_table2", "火影忍者"} {
		if _, ok := result[key]; !ok {
			t.Errorf("missing table key %q, got %+v", key, result)
		}
	}
	// 单表格游戏不附加_table后缀
	if _, ok := result["火影忍者_table1"]; ok {
		t.Error("single-table game should not have _table1 key")
	}
}

func TestScrapeAll_ContinuesAfterTableFailure(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		game := r.URL.Query().Get("game")
		if game == "绝区零" {
			w.WriteHeader(http.StatusUnauthorized) // 该游戏Cookie过期
			return
		}
		writeOrdersResponse(w, []map[string]interface{}{sampleOrderJSON(game)})
	})
	defer srv.Close()

	cfg := &config.Config{
		JYM: config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{
			Mode: "api",
			Games: []config.GameConfig{
				{Name: "绝区零", TableCount: 1},
				{Name: "鸣潮", TableCount: 1},
			},
		},
	}
	m := newTestManager(cfg, &models.CookieData{})

	// 失败的游戏应被跳过，其余游戏正常返回，且整体不报错
	result, err := m.ScrapeAll(context.Background())
	if err != nil {
		t.Fatalf("ScrapeAll should not fail when a single table fails: %v", err)
	}
	if _, ok := result["绝区零"]; ok {
		t.Error("failed table should not be in result")
	}
	if len(result["鸣潮"]) != 1 {
		t.Errorf("鸣潮 should have 1 order, got %d", len(result["鸣潮"]))
	}
}

func TestNewManager_NilInputs(t *testing.T) {
	// 所有入参为 nil 不应 panic
	m := NewManager(nil, nil, nil)
	if m == nil {
		t.Fatal("NewManager(nil,nil,nil) returned nil manager")
	}
	if _, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0); err == nil {
		t.Fatal("nil config should produce error from ScrapeGameTable")
	}
	if _, err := m.ScrapeAll(context.Background()); err == nil {
		t.Fatal("nil config should produce error from ScrapeAll")
	}
}
