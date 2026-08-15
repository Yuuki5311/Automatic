package scraper

import (
	"context"
	"errors"
	"fmt"
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

func TestScrapeDayDateLocal(t *testing.T) {
	d := ScrapeDayDate(time.Date(2026, 8, 15, 17, 5, 0, 0, time.Local))
	if d != "2026-08-15" {
		t.Fatal(d)
	}
}

func TestMonthStartDateLocal(t *testing.T) {
	d := MonthStartDate(time.Date(2026, 8, 15, 17, 0, 0, 0, time.Local))
	if d != "2026-08-01" {
		t.Fatal(d)
	}
	d2 := MonthStartDate(time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local))
	if d2 != "2026-09-01" {
		t.Fatal(d2)
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
			TimeKey:  BoardStatsTimeKey,
			Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "10"}},
		}, nil
	}}

	var reports []string
	m.SetResultReporter(func(tableKey string, count int, err error) {
		reports = append(reports, fmt.Sprintf("%s:%d:%v", tableKey, count, err != nil))
	})

	snap, err := m.ScrapeBoardAll(context.Background())
	if err != nil {
		t.Fatalf("generic game miss should soft-skip, not fail account: %v", err)
	}
	if snap.Date != ScrapeDayDate(time.Now()) {
		t.Fatalf("Date=%s want %s", snap.Date, ScrapeDayDate(time.Now()))
	}
	if len(snap.Games) != 1 {
		t.Fatalf("failed game should be skipped, games=%d %+v", len(snap.Games), snap.Games)
	}
	if snap.Games[0].GameName != "原神" || len(snap.Games[0].Metrics) != 1 || snap.Games[0].Error != "" {
		t.Fatalf("success game: %+v", snap.Games[0])
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
	if err != nil {
		t.Fatalf("unconfirmed misses should not mark account failed, got %v", err)
	}
	if len(snap.Games) != 0 {
		t.Fatalf("all failed games should be skipped, games=%d", len(snap.Games))
	}
}

func TestScrapeBoardAll_PrivilegeOnlyIsSuccess(t *testing.T) {
	cfg := &config.Config{
		Scraper: config.ScraperConfig{
			Games: []config.GameConfig{{Name: "原神"}, {Name: "鸣潮"}},
		},
	}
	m := NewManager(cfg, nil, nil)
	m.fetcher = stubBoardFetcher{fn: func(_ context.Context, _ string) (models.GameBoardStats, error) {
		return models.GameBoardStats{}, fmt.Errorf("%w: FAIL_BIZ_NO_PRIVILEGE", errNoPrivilege)
	}}
	snap, err := m.ScrapeBoardAll(context.Background())
	if err != nil {
		t.Fatalf("privilege-only should not fail: %v", err)
	}
	if len(snap.Games) != 0 {
		t.Fatalf("games=%d", len(snap.Games))
	}
}

func TestScrapeBoardAll_PartialHardFailKeepsOkGames(t *testing.T) {
	cfg := &config.Config{
		Scraper: config.ScraperConfig{
			Games: []config.GameConfig{{Name: "原神"}, {Name: "鸣潮"}, {Name: "绝区零"}},
		},
	}
	m := NewManager(cfg, nil, nil)
	m.fetcher = stubBoardFetcher{fn: func(_ context.Context, gameName string) (models.GameBoardStats, error) {
		switch gameName {
		case "鸣潮":
			return models.GameBoardStats{}, fmt.Errorf("%w: x", errNoPrivilege)
		case "绝区零":
			return models.GameBoardStats{}, fmt.Errorf("%w: 解析损坏", errConfirmedGameQuery)
		default:
			return models.GameBoardStats{
				GameName: gameName,
				Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "1"}},
			}, nil
		}
	}}
	snap, err := m.ScrapeBoardAll(context.Background())
	var hard *HardGameScrapeError
	if !errors.As(err, &hard) || len(hard.Games) != 1 || hard.Games[0] != "绝区零" {
		t.Fatalf("hard=%v err=%v", hard, err)
	}
	if len(snap.Games) != 1 || snap.Games[0].GameName != "原神" {
		t.Fatalf("snap=%+v", snap.Games)
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
			TimeKey:  BoardStatsTimeKey,
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
