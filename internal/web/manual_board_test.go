package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/scrapehistory"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestBoardGamesAPI(t *testing.T) {
	s, err := New(status.NewStore(), &config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/api/board/games")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Games []string `json:"games"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Games) < 1 {
		t.Fatalf("games=%v", out.Games)
	}
}

func TestBoardManualWritesFeishu(t *testing.T) {
	dir := t.TempDir()
	acctStore := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = acctStore.Load()
	_, _, err := acctStore.UpsertFromRemoteKey("13800000001", "pw", "店A", 1, 1, "cat")
	if err != nil {
		t.Fatal(err)
	}

	hist := scrapehistory.NewStore(filepath.Join(dir, "scrape_history.json"))
	_ = hist.Load()
	st := status.NewStore()
	cfg := &config.Config{
		Feishu: config.FeishuConfig{
			AppID: "cli_test", AppSecret: "sec",
			BitableID: "basX", BoardTableID: "tblX",
		},
	}
	s, err := New(st, cfg, nil, nil, nil, acctStore)
	if err != nil {
		t.Fatal(err)
	}
	s.SetHistoryStore(hist)

	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/board/manual", "application/json",
		strings.NewReader(`{"date":"2026-08-14","account":"nope","game":"原神","metrics":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 got %d", resp.StatusCode)
	}

	resp, err = http.Post(ts.URL+"/api/board/manual", "application/json",
		strings.NewReader(`{"date":"bad","account":"13800000001","game":"原神","metrics":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", resp.StatusCode, body)
	}
}

func TestDashboardHistoryManualLink(t *testing.T) {
	store := status.NewStore()
	store.SetScrapeHistory([]status.HistoryEntry{
		{Account: "13800000000", At: time.Now(), Status: "skipped", Error: "cookie expired"},
	})
	s, err := New(store, &config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		"openManualSupplement",
		"hist-fail",
		"手工补充飞书数据",
		"/api/board/manual",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
