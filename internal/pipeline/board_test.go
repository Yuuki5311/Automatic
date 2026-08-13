package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
	"github.com/example/jiaoyimao-scraper/internal/statsstore"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestBoardRun_SkipsFailedAccountContinues(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	a1, _ := as.Add("bad", "x")
	a2, _ := as.Add("good", "y")
	st := status.NewStore()
	var scraped []string
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(ctx context.Context, acct accounts.Account) (*models.CookieData, error) {
			if acct.Username == "bad" {
				return nil, errors.New("login failed")
			}
			return &models.CookieData{
				Cookies:   []models.CookieEntry{{Name: "token", Value: "1"}},
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil
		},
		ScrapeAccount: func(ctx context.Context, acct accounts.Account, c *models.CookieData) (models.BoardStatsSnapshot, error) {
			scraped = append(scraped, acct.Username)
			return models.BoardStatsSnapshot{
				Date: "2026-08-12", Account: acct.Username,
				Games: []models.GameBoardStats{{GameName: "原神"}},
			}, nil
		},
		Save: statsstore.Save,
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if len(scraped) != 1 || scraped[0] != "good" {
		t.Fatalf("scraped=%v", scraped)
	}
	bad, _ := as.Get(a1.ID)
	good, _ := as.Get(a2.ID)
	if bad.LastStatus != "skipped" || good.LastStatus != "ok" {
		t.Fatalf("bad=%+v good=%+v", bad, good)
	}
}

func TestBoardRunRecordsMetricsAndStatsDate(t *testing.T) {
	st := status.NewStore()
	dir := t.TempDir()
	as := testAccountStore(t, "acc1")
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: dir}},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return validCookie(), nil
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
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
	if _, err := os.Stat(filepath.Join(dir, "2026-08-12", "acc1.json")); err != nil {
		t.Fatalf("expected saved snapshot: %v", err)
	}
}

func TestBoardRunCookieFailureMarksLoginFailed(t *testing.T) {
	st := status.NewStore()
	as := testAccountStore(t, "only")
	acct := as.Enabled()[0]
	r := &BoardRun{
		Cfg:      &config.Config{},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return nil, errors.New("login denied")
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
			t.Fatal("Scrape should not be called")
			return models.BoardStatsSnapshot{}, nil
		},
	}

	r.Run()

	got, ok := as.Get(acct.ID)
	if !ok || got.LastStatus != "skipped" || got.LastError != "login denied" {
		t.Fatalf("account=%+v ok=%v", got, ok)
	}
	snap := st.Snapshot()
	if snap.LastRun == nil || !snap.LastRun.Success {
		t.Fatalf("overall run should finish after skip: %+v", snap.LastRun)
	}
}

func TestBoardRunKeepsMetricsOnSaveFailure(t *testing.T) {
	st := status.NewStore()
	as := testAccountStore(t, "acc1")
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: t.TempDir()}},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return validCookie(), nil
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
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
	if snap.LastRun == nil || !snap.LastRun.Success {
		t.Fatalf("overall run should finish after skip: %+v", snap.LastRun)
	}
	got := as.Enabled()[0]
	fresh, _ := as.Get(got.ID)
	if fresh.LastStatus != "skipped" || fresh.LastError != "disk full" {
		t.Fatalf("account=%+v", fresh)
	}
}

func TestBoardRunSecondCallerGetsAlreadyRunning(t *testing.T) {
	st := status.NewStore()
	as := testAccountStore(t, "acc1")
	started := make(chan struct{})
	release := make(chan struct{})
	r := &BoardRun{
		Cfg:      &config.Config{},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return validCookie(), nil
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
			close(started)
			<-release
			return models.BoardStatsSnapshot{Date: "2026-08-12"}, nil
		},
		Save: func(string, models.BoardStatsSnapshot) (string, error) {
			return "ok", nil
		},
	}

	var firstErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstErr = r.Run()
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first Run did not start scrape")
	}

	err := r.Run()
	if err == nil || err.Error() != "scrape already running" {
		t.Fatalf("second Run err=%v, want %q", err, "scrape already running")
	}

	close(release)
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("first Run: %v", firstErr)
	}
}

func testAccountStore(t *testing.T, usernames ...string) *accounts.Store {
	t.Helper()
	as := accounts.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err := as.Load(); err != nil {
		t.Fatal(err)
	}
	for _, u := range usernames {
		if _, err := as.Add(u, "pw"); err != nil {
			t.Fatal(err)
		}
	}
	return as
}

func validCookie() *models.CookieData {
	return &models.CookieData{
		Cookies:   []models.CookieEntry{{Name: "token", Value: "sess"}},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
}
