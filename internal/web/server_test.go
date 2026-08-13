package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestHandleScrapePOSTStartsAsync(t *testing.T) {
	store := status.NewStore()
	started := make(chan struct{})
	s := &Server{
		store: store,
		scrapeFn: func() {
			close(started)
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/scrape", nil)
	rec := httptest.NewRecorder()
	s.handleScrape(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "started" {
		t.Fatalf("body=%v", body)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("scrapeFn was not invoked")
	}
}

func TestHandleScrapeGETNotAllowed(t *testing.T) {
	s := &Server{store: status.NewStore(), scrapeFn: func() {}}
	req := httptest.NewRequest(http.MethodGet, "/api/scrape", nil)
	rec := httptest.NewRecorder()
	s.handleScrape(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleScrapeNilFnServiceUnavailable(t *testing.T) {
	s := &Server{store: status.NewStore()}
	req := httptest.NewRequest(http.MethodPost, "/api/scrape", nil)
	rec := httptest.NewRecorder()
	s.handleScrape(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleScrapeAlreadyRunning(t *testing.T) {
	store := status.NewStore()
	store.RunStarted()
	var calls atomic.Int32
	s := &Server{store: store, scrapeFn: func() { calls.Add(1) }}

	req := httptest.NewRequest(http.MethodPost, "/api/scrape", nil)
	rec := httptest.NewRecorder()
	s.handleScrape(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "already_running" {
		t.Fatalf("body=%v", body)
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("scrapeFn should not run when already scraping")
	}
}

func TestNewRegistersScrapeRoute(t *testing.T) {
	store := status.NewStore()
	called := make(chan struct{})
	s, err := New(store, &config.Config{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetScrapeFunc(func() { close(called) })

	ts := httptest.NewServer(s.srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/scrape", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("mux did not invoke scrapeFn")
	}
}

func TestDashboardRendersBoardMetricsAndActions(t *testing.T) {
	store := status.NewStore()
	store.RunStarted()
	store.SetStatsDate("2026-08-12")
	store.RecordGameStats("原神", []models.BoardMetric{
		{Title: "咨询量", Value: "186"},
		{Title: "回收成功金额", Value: "9650.00", Unit: "元"},
	}, nil)
	store.RunFinished(nil, 1)

	s, err := New(store, &config.Config{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"咨询量", "186", "回收成功金额", "9650.00", "元",
		"立即抓取", "下次定时：每天 09:00",
		"本阶段未启用",
		"2026-08-12",
		"/api/scrape",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardLoginFailedRedStyle(t *testing.T) {
	store := status.NewStore()
	store.SetLoginPhase(status.LoginFailed, "账号或密码错误")

	s, err := New(store, &config.Config{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "账号或密码错误") {
		t.Fatal("missing login error")
	}
	if !strings.Contains(body, "login-failed") {
		t.Fatal("cookie/login sections should use login-failed class for red styling")
	}
}
