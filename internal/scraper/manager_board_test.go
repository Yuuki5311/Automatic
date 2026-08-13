package scraper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

type stubBoardFetcher struct {
	fn func(ctx context.Context, gameName string) (models.GameBoardStats, error)
}

func (s stubBoardFetcher) FetchBoardStats(ctx context.Context, gameName string) (models.GameBoardStats, error) {
	return s.fn(ctx, gameName)
}

func TestYesterdayDateLocal(t *testing.T) {
	d := YesterdayDate(time.Date(2026, 8, 13, 10, 0, 0, 0, time.Local))
	if d != "2026-08-12" {
		t.Fatal(d)
	}
}

func TestScrapeBoardAll_PartialFailureContinues(t *testing.T) {
	cfg := &config.Config{
		Scraper: config.ScraperConfig{
			Games: []config.GameConfig{
				{Name: "原神"},
				{Name: "鸣潮"},
			},
		},
	}
	m := NewManager(cfg, nil, nil)
	m.fetcher = stubBoardFetcher{fn: func(_ context.Context, gameName string) (models.GameBoardStats, error) {
		if gameName == "鸣潮" {
			return models.GameBoardStats{}, errors.New("boom")
		}
		return models.GameBoardStats{
			GameName: gameName,
			GameID:   gameIDGenshin,
			TimeKey:  "yesterday",
			Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "10"}},
		}, nil
	}}

	var reports []string
	m.SetResultReporter(func(tableKey string, count int, err error) {
		reports = append(reports, fmt.Sprintf("%s:%d:%v", tableKey, count, err != nil))
	})

	snap, err := m.ScrapeBoardAll(context.Background())
	if err != nil {
		t.Fatalf("partial failure should not fail the run: %v", err)
	}
	if snap.Date != YesterdayDate(time.Now()) {
		t.Fatalf("Date=%s want %s", snap.Date, YesterdayDate(time.Now()))
	}
	if len(snap.Games) != 2 {
		t.Fatalf("games=%d", len(snap.Games))
	}
	if snap.Games[0].GameName != "原神" || len(snap.Games[0].Metrics) != 1 || snap.Games[0].Error != "" {
		t.Fatalf("success game: %+v", snap.Games[0])
	}
	if snap.Games[1].GameName != "鸣潮" || snap.Games[1].Error == "" || snap.Games[1].GameID != gameIDWuthering {
		t.Fatalf("failed game should keep name/id/error: %+v", snap.Games[1])
	}
	if len(reports) != 2 {
		t.Fatalf("reports=%v", reports)
	}
}

func TestScrapeBoardAll_AllFailReturnsError(t *testing.T) {
	cfg := &config.Config{
		Scraper: config.ScraperConfig{
			Games: []config.GameConfig{{Name: "原神"}, {Name: "鸣潮"}},
		},
	}
	m := NewManager(cfg, nil, nil)
	m.fetcher = stubBoardFetcher{fn: func(_ context.Context, _ string) (models.GameBoardStats, error) {
		return models.GameBoardStats{}, errors.New("down")
	}}

	snap, err := m.ScrapeBoardAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "全部游戏看板抓取失败") {
		t.Fatalf("want all-fail error, got %v", err)
	}
	if len(snap.Games) != 2 {
		t.Fatalf("should still return per-game errors, games=%d", len(snap.Games))
	}
	for _, g := range snap.Games {
		if g.Error == "" {
			t.Fatalf("expected Error on %+v", g)
		}
	}
}

func TestScrapeBoardAll_NilConfig(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if _, err := m.ScrapeBoardAll(context.Background()); err == nil {
		t.Fatal("nil config should error")
	}
}

func TestScrapeBoardAll_SessionExpiredRefreshesAndRetries(t *testing.T) {
	cfg := &config.Config{
		Scraper: config.ScraperConfig{Games: []config.GameConfig{{Name: "原神"}}},
	}
	m := NewManager(cfg, nil, nil)
	calls := 0
	m.fetcher = stubBoardFetcher{fn: func(_ context.Context, gameName string) (models.GameBoardStats, error) {
		calls++
		if calls == 1 {
			return models.GameBoardStats{}, errCookieExpired
		}
		return models.GameBoardStats{
			GameName: gameName,
			TimeKey:  "yesterday",
			Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "1"}},
		}, nil
	}}
	refreshes := 0
	m.SetSessionRefresher(func(ctx context.Context) (*models.CookieData, error) {
		refreshes++
		return &models.CookieData{}, nil
	})

	snap, err := m.ScrapeBoardAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || calls != 2 {
		t.Fatalf("refreshes=%d calls=%d", refreshes, calls)
	}
	if len(snap.Games) != 1 || snap.Games[0].Error != "" || len(snap.Games[0].Metrics) != 1 {
		t.Fatalf("%+v", snap.Games)
	}
}
