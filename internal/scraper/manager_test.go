package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

const mtopStatsPath = "/h5/mtop.com.jym.merchant.board.recyclestats/1.0/"

// testAPIServer 模拟 MTOP recyclestats，记录请求便于断言。
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

func useTestMTOP(t *testing.T, srvURL string) {
	t.Helper()
	orig := mtopBaseURL
	mtopBaseURL = srvURL
	t.Cleanup(func() { mtopBaseURL = orig })
}

func testMTOPCookies() *models.CookieData {
	return &models.CookieData{Cookies: []models.CookieEntry{
		{Name: "_m_h5_tk", Value: "abc_123", Domain: ".jiaoyimao.com", Path: "/"},
		{Name: "token", Value: "secret", Domain: ".jiaoyimao.com", Path: "/"},
	}}
}

func writeMTOPSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ret":["SUCCESS::调用成功"],"data":{"result":[{"title":"咨询量","staData":"10","properties":{"tips":"t"}}]}}`))
}

func writeMTOPSessionExpired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ret":["FAIL_SYS_SESSION_EXPIRED::x"],"data":{}}`))
}

func newTestManager(cfg *config.Config, cookies *models.CookieData) *Manager {
	return NewManager(cfg, nil, cookies)
}

func TestScrapeGameTable_APIModeSuccess(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM: config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{
			Mode:  "api",
			Games: []config.GameConfig{{Name: "原神", TableCount: 1}},
		},
	}
	cookies := testMTOPCookies()
	m := newTestManager(cfg, cookies)

	orders, err := m.ScrapeGameTable(context.Background(), cfg.Scraper.Games[0], 0)
	if err != nil {
		t.Fatalf("ScrapeGameTable(api mode) failed: %v", err)
	}
	// 订单列表 API 已停用：FetchRecycleOrders 在看板成功后返回空列表。
	if len(orders) != 0 {
		t.Fatalf("deprecated order fetch should return empty, got %+v", orders)
	}
	if !srv.hasRequest(http.MethodGet, mtopStatsPath) {
		t.Fatal("GET recyclestats was never called")
	}
	var got *testAPIRequest
	for i := range srv.requests {
		r := &srv.requests[i]
		if r.method == http.MethodGet && r.path == mtopStatsPath {
			got = r
			break
		}
	}
	tokenFound := false
	for _, c := range got.cookies {
		if c.Name == "_m_h5_tk" && c.Value == "abc_123" {
			tokenFound = true
		}
	}
	if !tokenFound {
		t.Error("_m_h5_tk cookie was not forwarded to MTOP")
	}
}

func TestScrapeGameTable_APIModeExpiredCookie(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		writeMTOPSessionExpired(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "api"},
	}
	m := newTestManager(cfg, testMTOPCookies())

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
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, testMTOPCookies())

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil {
		t.Fatal("expected error from browser fallback path, got nil")
	}
	if !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("error should come from browser fallback, got: %v", err)
	}
}

func TestScrapeGameTable_AutoModeAPISuccessNoBrowser(t *testing.T) {
	// 看板 FetchRecycleOrders 成功后仍返回空订单，auto 会落入浏览器兜底。
	// 此测试改为验证 api 模式在 MTOP 成功时不走浏览器。
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "api"},
	}
	m := newTestManager(cfg, testMTOPCookies())

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err != nil {
		t.Fatalf("api mode should succeed via MTOP without browser: %v", err)
	}
}

func TestScrapeGameTable_AutoModeEmptyListFallsBackToBrowser(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, testMTOPCookies())

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("empty API list should fall back to browser, got err: %v", err)
	}
}

func TestScrapeGameTable_AutoModeProbeFailureFallsBackToBrowser(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, testMTOPCookies())

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("probe failure should fall back to browser, got err: %v", err)
	}
	if !srv.hasRequest(http.MethodGet, mtopStatsPath) {
		t.Fatal("ProbeAPI GET request to recyclestats was not sent")
	}
}

func TestScrapeGameTable_AutoModeAuthErrorRefreshesAndRetries(t *testing.T) {
	var fetchCalls int
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		fetchCalls++
		// ProbeAPI 只看 HTTP 状态；业务解析在 FetchBoardStats。
		// 第 1 轮：探测 200 + 业务 SESSION；刷新后第 2 轮成功但仍返回空订单 → 浏览器兜底。
		if fetchCalls <= 2 {
			writeMTOPSessionExpired(w)
			return
		}
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "auto"},
	}
	m := newTestManager(cfg, testMTOPCookies())

	refreshes := 0
	m.SetSessionRefresher(func(ctx context.Context) (*models.CookieData, error) {
		refreshes++
		return testMTOPCookies(), nil
	})

	_, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0)
	if refreshes != 1 {
		t.Fatalf("expected exactly 1 refresh, got %d", refreshes)
	}
	// 订单列表已停用：刷新后 API 仍返回空列表，auto 落入浏览器。
	if err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatalf("after refresh, empty orders should fall back to browser, got: %v", err)
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
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: ""},
	}
	m := newTestManager(cfg, testMTOPCookies())

	// 空 mode 按 auto：MTOP 成功但订单为空 → 浏览器兜底（区别于 api 模式不报错）。
	if _, err := m.ScrapeGameTable(context.Background(), config.GameConfig{Name: "原神"}, 0); err == nil || !strings.Contains(err.Error(), "浏览器") {
		t.Fatal("empty mode should default to auto and fall back to browser on empty orders")
	}
}

func TestScrapeGame_MergesAllTables(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM: config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{
			Mode:  "api",
			Games: []config.GameConfig{{Name: "原神", TableCount: 2}},
		},
	}
	m := newTestManager(cfg, testMTOPCookies())

	orders, err := m.ScrapeGame(context.Background(), cfg.Scraper.Games[0])
	if err != nil {
		t.Fatalf("ScrapeGame failed: %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("deprecated order fetch should return empty, got %d", len(orders))
	}
}

func TestScrapeGame_TableFailureReturnsError(t *testing.T) {
	var n int
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		n++
		// 每表 ProbeAPI + FetchBoardStats；第 2 表从第 3 次请求起失败。
		if n >= 3 {
			writeMTOPSessionExpired(w)
			return
		}
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

	cfg := &config.Config{
		JYM:     config.JYMConfig{BaseURL: srv.srv.URL},
		Scraper: config.ScraperConfig{Mode: "api"},
	}
	m := newTestManager(cfg, testMTOPCookies())

	if _, err := m.ScrapeGame(context.Background(), config.GameConfig{Name: "原神", TableCount: 2}); err == nil {
		t.Fatal("ScrapeGame should return error when a table fails")
	}
}

func TestScrapeAll_TableKeys(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

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
	m := newTestManager(cfg, testMTOPCookies())

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
	if _, ok := result["火影忍者_table1"]; ok {
		t.Error("single-table game should not have _table1 key")
	}
}

func TestScrapeAll_ContinuesAfterTableFailure(t *testing.T) {
	srv := newTestAPIServer(func(w http.ResponseWriter, r *http.Request) {
		data := r.URL.Query().Get("data")
		if strings.Contains(data, "1013597") { // 绝区零
			writeMTOPSessionExpired(w)
			return
		}
		writeMTOPSuccess(w)
	})
	defer srv.Close()
	useTestMTOP(t, srv.srv.URL)

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
	m := newTestManager(cfg, testMTOPCookies())

	result, err := m.ScrapeAll(context.Background())
	if err != nil {
		t.Fatalf("ScrapeAll should not fail when a single table fails: %v", err)
	}
	if _, ok := result["绝区零"]; ok {
		t.Error("failed table should not be in result")
	}
	if _, ok := result["鸣潮"]; !ok {
		t.Error("鸣潮 should be present after sibling table failure")
	}
}

func TestNewManager_NilInputs(t *testing.T) {
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
