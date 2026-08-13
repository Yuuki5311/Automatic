package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
	"github.com/example/jiaoyimao-scraper/internal/statsstore"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestBoardRunRecordsMetricsAndStatsDate(t *testing.T) {
	st := status.NewStore()
	dir := t.TempDir()
	r := &BoardRun{
		Cfg:   &config.Config{Scraper: config.ScraperConfig{StatsDir: dir}},
		Store: st,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepCookies: func(context.Context) (*models.CookieData, error) {
			return validCookie(), nil
		},
		Scrape: func(context.Context, *models.CookieData) (models.BoardStatsSnapshot, error) {
			return models.BoardStatsSnapshot{
				Date: "2026-08-12",
				Games: []models.GameBoardStats{{
					GameName: "原神",
					Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "186", Unit: ""}},
				}},
			}, nil
		},
		Save: statsstore.Save,
	}

	r.Run()

	snap := st.Snapshot()
	if snap.LastRun == nil || !snap.LastRun.Success {
		t.Fatalf("LastRun=%+v", snap.LastRun)
	}
	if snap.LastRun.StatsDate != "2026-08-12" {
		t.Fatalf("StatsDate=%q", snap.LastRun.StatsDate)
	}
	if snap.LastRun.TotalOrders != 1 {
		t.Fatalf("TotalOrders=%d", snap.LastRun.TotalOrders)
	}
	if len(snap.Games) != 1 || snap.Games[0].GameName != "原神" || snap.Games[0].RecordCount != 1 {
		t.Fatalf("games=%+v", snap.Games)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-12.json")); err != nil {
		t.Fatalf("expected saved snapshot: %v", err)
	}
}

func TestBoardRunCookieFailureMarksLoginFailed(t *testing.T) {
	st := status.NewStore()
	r := &BoardRun{
		Cfg:   &config.Config{},
		Store: st,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepCookies: func(context.Context) (*models.CookieData, error) {
			return nil, errors.New("login denied")
		},
		Scrape: func(context.Context, *models.CookieData) (models.BoardStatsSnapshot, error) {
			t.Fatal("Scrape should not be called")
			return models.BoardStatsSnapshot{}, nil
		},
	}

	r.Run()

	snap := st.Snapshot()
	if snap.LoginPhase != status.LoginFailed {
		t.Fatalf("LoginPhase=%q", snap.LoginPhase)
	}
	if snap.LoginError != "login denied" {
		t.Fatalf("LoginError=%q", snap.LoginError)
	}
	if snap.LastRun == nil || snap.LastRun.Success {
		t.Fatalf("LastRun should be failed: %+v", snap.LastRun)
	}
}

func TestBoardRunKeepsMetricsOnSaveFailure(t *testing.T) {
	st := status.NewStore()
	r := &BoardRun{
		Cfg:   &config.Config{Scraper: config.ScraperConfig{StatsDir: t.TempDir()}},
		Store: st,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepCookies: func(context.Context) (*models.CookieData, error) {
			return validCookie(), nil
		},
		Scrape: func(context.Context, *models.CookieData) (models.BoardStatsSnapshot, error) {
			return models.BoardStatsSnapshot{
				Date: "2026-08-12",
				Games: []models.GameBoardStats{{
					GameName: "鸣潮",
					Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "3"}},
				}},
			}, nil
		},
		Save: func(string, models.BoardStatsSnapshot) (string, error) {
			return "", errors.New("disk full")
		},
	}

	r.Run()

	snap := st.Snapshot()
	if len(snap.Games) != 1 || snap.Games[0].GameName != "鸣潮" {
		t.Fatalf("expected in-memory game result: %+v", snap.Games)
	}
	if snap.LastRun == nil || snap.LastRun.Success || snap.LastRun.Error != "disk full" {
		t.Fatalf("LastRun=%+v", snap.LastRun)
	}
}

func validCookie() *models.CookieData {
	return &models.CookieData{
		Cookies:   []models.CookieEntry{{Name: "token", Value: "sess"}},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
}
