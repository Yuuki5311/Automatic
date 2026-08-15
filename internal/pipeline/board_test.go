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
	"github.com/example/jiaoyimao-scraper/internal/scrapehistory"
	"github.com/example/jiaoyimao-scraper/internal/scraper"
	"github.com/example/jiaoyimao-scraper/internal/statsstore"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestBoardRun_SyncCookiesBeforeRunCalled(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	_, _ = as.Add("u1", "p")
	st := status.NewStore()
	calls := 0
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		SyncCookiesBeforeRun: func() (int, error) {
			calls++
			return 3, nil
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return validCookie(), nil
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
			return models.BoardStatsSnapshot{
				Date: "2026-08-12",
				Games: []models.GameBoardStats{{
					GameName: "原神",
					Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "1"}},
				}},
			}, nil
		},
		Save: statsstore.Save,
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("SyncCookiesBeforeRun calls=%d", calls)
	}
}

func TestBoardRun_SkipsFailedAccountContinues(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	a1, _ := as.Add("bad", "x")
	a2, _ := as.Add("good", "y")
	st := status.NewStore()
	hist := scrapehistory.NewStore(filepath.Join(dir, "scrape_history.json"))
	_ = hist.Load()
	var scraped []string
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store:    st,
		Accounts: as,
		History:  hist,
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
	snap := st.Snapshot()
	if snap.LastRun == nil || !snap.LastRun.Success {
		t.Fatalf("partial skip should succeed: %+v", snap.LastRun)
	}
	if snap.LastRun.OkAccounts != 1 || snap.LastRun.SkippedAccounts != 1 {
		t.Fatalf("ok=%d skipped=%d", snap.LastRun.OkAccounts, snap.LastRun.SkippedAccounts)
	}
	if len(snap.ScrapeHistory) != 2 {
		t.Fatalf("history=%+v", snap.ScrapeHistory)
	}
	// newest first: good then bad
	if snap.ScrapeHistory[0].Account != "good" || snap.ScrapeHistory[0].Status != "ok" {
		t.Fatalf("hist0=%+v", snap.ScrapeHistory[0])
	}
	if snap.ScrapeHistory[1].Account != "bad" || snap.ScrapeHistory[1].Status != "failed" || snap.ScrapeHistory[1].Error != "登录失败" {
		t.Fatalf("hist1=%+v", snap.ScrapeHistory[1])
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
	if snap.LastRun.OkAccounts != 1 || snap.LastRun.SkippedAccounts != 0 {
		t.Fatalf("ok=%d skipped=%d", snap.LastRun.OkAccounts, snap.LastRun.SkippedAccounts)
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
	if !ok || got.LastStatus != "skipped" || got.LastError != "登录失败" {
		t.Fatalf("account=%+v ok=%v", got, ok)
	}
	snap := st.Snapshot()
	if snap.LastRun == nil || snap.LastRun.Success {
		t.Fatalf("all skipped should fail: %+v", snap.LastRun)
	}
	if snap.LastRun.Error != "全部账户跳过" {
		t.Fatalf("Error=%q", snap.LastRun.Error)
	}
	if snap.LastRun.OkAccounts != 0 || snap.LastRun.SkippedAccounts != 1 {
		t.Fatalf("ok=%d skipped=%d", snap.LastRun.OkAccounts, snap.LastRun.SkippedAccounts)
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
	if snap.LastRun == nil || snap.LastRun.Success {
		t.Fatalf("all skipped should fail: %+v", snap.LastRun)
	}
	if snap.LastRun.Error != "全部账户跳过" {
		t.Fatalf("Error=%q", snap.LastRun.Error)
	}
	if snap.LastRun.OkAccounts != 0 || snap.LastRun.SkippedAccounts != 1 {
		t.Fatalf("ok=%d skipped=%d", snap.LastRun.OkAccounts, snap.LastRun.SkippedAccounts)
	}
	got := as.Enabled()[0]
	fresh, _ := as.Get(got.ID)
	if fresh.LastStatus != "skipped" || fresh.LastError != "disk full" {
		t.Fatalf("account=%+v", fresh)
	}
}

func TestBoardRun_CookieFailRefreshStillFailDisables(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	a, _ := as.Add("u1", "p")
	st := status.NewStore()
	refreshCalls := 0
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store:    st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return validCookie(), nil
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
			return models.BoardStatsSnapshot{}, errors.New("Cookie 失效 / SESSION expired")
		},
		RefreshCookies: func(acct accounts.Account) (*models.CookieData, error) {
			refreshCalls++
			return nil, errors.New("leyoo empty")
		},
		Save: statsstore.Save,
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refreshCalls=%d", refreshCalls)
	}
	got, _ := as.Get(a.ID)
	if got.Enabled {
		t.Fatalf("should disable after refresh fail: %+v", got)
	}
	if got.LastError != "登录失败" {
		t.Fatalf("LastError=%q", got.LastError)
	}
}

func TestBoardRun_HardGameFailStillSavesAndMarksFailed(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	_, _ = as.Add("u1", "p")
	st := status.NewStore()
	hist := scrapehistory.NewStore(filepath.Join(dir, "scrape_history.json"))
	_ = hist.Load()
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store:    st,
		Accounts: as,
		History:  hist,
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
					Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "1"}},
				}},
			}, &scraper.HardGameScrapeError{Games: []string{"鸣潮"}}
		},
		Save: statsstore.Save,
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stats", "2026-08-12", "u1.json")); err != nil {
		t.Fatalf("should save ok games: %v", err)
	}
	snap := st.Snapshot()
	if len(snap.ScrapeHistory) != 1 || snap.ScrapeHistory[0].Status != "failed" || snap.ScrapeHistory[0].Error != "抓取鸣潮失败" {
		t.Fatalf("hist=%+v", snap.ScrapeHistory)
	}
	if snap.LastRun == nil || !snap.LastRun.Success || snap.LastRun.OkAccounts != 1 {
		t.Fatalf("last=%+v", snap.LastRun)
	}
}

func TestBoardRun_PrivilegeOnlyNotFailed(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	_, _ = as.Add("u1", "p")
	st := status.NewStore()
	hist := scrapehistory.NewStore(filepath.Join(dir, "scrape_history.json"))
	_ = hist.Load()
	r := &BoardRun{
		Cfg:      &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store:    st,
		Accounts: as,
		History:  hist,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(context.Context, accounts.Account) (*models.CookieData, error) {
			return validCookie(), nil
		},
		ScrapeAccount: func(context.Context, accounts.Account, *models.CookieData) (models.BoardStatsSnapshot, error) {
			return models.BoardStatsSnapshot{Date: "2026-08-12"}, nil
		},
		Save: statsstore.Save,
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()
	if len(snap.ScrapeHistory) != 1 || snap.ScrapeHistory[0].Status != "ok" || snap.ScrapeHistory[0].Error != "" {
		t.Fatalf("privilege-only should be ok hist=%+v", snap.ScrapeHistory)
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

func TestNewBoardRunWiresAccounts(t *testing.T) {
	as := accounts.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	st := status.NewStore()
	r := NewBoardRun(&config.Config{}, nil, nil, nil, st, as)
	if r.Accounts != as {
		t.Fatal("NewBoardRun should set Accounts")
	}
	if r.Store != st {
		t.Fatal("NewBoardRun should set Store")
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
