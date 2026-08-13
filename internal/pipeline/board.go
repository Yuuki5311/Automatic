package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/feishu"
	"github.com/example/jiaoyimao-scraper/internal/models"
	"github.com/example/jiaoyimao-scraper/internal/scraper"
	"github.com/example/jiaoyimao-scraper/internal/statsstore"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

// BoardRun 看板抓取编排（Cookie → ScrapeBoardAll → 落盘 → 飞书 → 更新 status）。
// 由 cmd/scraper 与 Web /api/scrape 共用，避免复制 runScrape。
type BoardRun struct {
	Cfg        *config.Config
	Store      *status.Store
	Accounts   *accounts.Store
	LoginSvc   *auth.LoginService
	Solver     captcha.Solver
	Browser    *browser.Manager
	NewContext func(timeoutSec int) (context.Context, context.CancelFunc)
	// PrepAccountCookies / ScrapeAccount are test hooks; NewBoardRun wires production defaults.
	PrepAccountCookies func(ctx context.Context, acct accounts.Account) (*models.CookieData, error)
	ScrapeAccount      func(ctx context.Context, acct accounts.Account, cookies *models.CookieData) (models.BoardStatsSnapshot, error)
	Save               func(dir string, snap models.BoardStatsSnapshot) (string, error)
	SyncFeishu         func(ctx context.Context, snap models.BoardStatsSnapshot) (newCount, updCount int, err error)

	mu sync.Mutex
}

// ErrScrapeRunning 已有一轮看板抓取在执行（HTTP 与 cron 共用同一把锁）。
var ErrScrapeRunning = errors.New("scrape already running")

// NewBoardRun 组装真实依赖的看板抓取流程。
func NewBoardRun(cfg *config.Config, browserMgr *browser.Manager, loginSvc *auth.LoginService, solver captcha.Solver, st *status.Store, acctStore *accounts.Store) *BoardRun {
	r := &BoardRun{
		Cfg:      cfg,
		Store:    st,
		Accounts: acctStore,
		NewContext: func(timeoutSec int) (context.Context, context.CancelFunc) {
			return browserMgr.NewContext(timeoutSec)
		},
		LoginSvc: loginSvc,
		Solver:   solver,
		Browser:  browserMgr,
		PrepAccountCookies: func(ctx context.Context, acct accounts.Account) (*models.CookieData, error) {
			acctCfg := cfg.WithJYMAccount(acct.Username, acct.Password, acct.CookiePath)
			return loginSvc.RefreshIfNeeded(ctx, acctCfg, solver)
		},
		ScrapeAccount: func(ctx context.Context, acct accounts.Account, cookies *models.CookieData) (models.BoardStatsSnapshot, error) {
			acctCfg := cfg.WithJYMAccount(acct.Username, acct.Password, acct.CookiePath)
			mgr := scraper.NewManager(acctCfg, browserMgr, cookies)
			mgr.SetSessionRefresher(func(refreshCtx context.Context) (*models.CookieData, error) {
				newC, refreshErr := loginSvc.RefreshIfNeeded(refreshCtx, acctCfg, solver)
				if refreshErr == nil {
					st.SetCookie(newC, auth.IsCookieValid(newC))
				}
				return newC, refreshErr
			})
			mgr.SetResultReporter(func(tableKey string, count int, reportErr error) {
				st.RecordGameStats(tableKey, nil, reportErr)
			})
			return mgr.ScrapeBoardAll(ctx)
		},
		Save: statsstore.Save,
	}
	if cfg != nil && cfg.Feishu.AppID != "" && cfg.Feishu.AppSecret != "" &&
		cfg.Feishu.BitableID != "" && cfg.Feishu.BoardTableID != "" &&
		!strings.Contains(cfg.Feishu.AppID, "xxxx") {
		client := feishu.NewClient(&cfg.Feishu)
		tableID := cfg.Feishu.BoardTableID
		r.SyncFeishu = func(ctx context.Context, snap models.BoardStatsSnapshot) (int, int, error) {
			return feishu.NewBitableOps(client, cfg.Feishu.BitableID).SyncBoardStats(ctx, tableID, snap)
		}
	}
	return r
}

// Run 执行一轮看板抓取。并发第二次调用立即返回 ErrScrapeRunning。
func (r *BoardRun) Run() error {
	if !r.mu.TryLock() {
		return ErrScrapeRunning
	}
	defer r.mu.Unlock()

	slog.Info("========== 开始抓取 ==========", "component", "pipeline")
	startTime := time.Now()

	r.Store.RunStarted()

	timeout := 0
	statsDir := ""
	if r.Cfg != nil {
		timeout = r.Cfg.Browser.TimeoutSec
		statsDir = r.Cfg.Scraper.StatsDir
	}

	var accts []accounts.Account
	if r.Accounts != nil {
		accts = r.Accounts.Enabled()
	}
	if len(accts) == 0 {
		err := errors.New("没有可用账户，请先在 UI 添加账号")
		slog.Error("没有可用账户", "component", "pipeline", "error", err)
		r.Store.RunFinished(err, 0)
		return nil
	}

	okGames := 0
	lastPath := ""
	for _, acct := range accts {
		n, path := r.runAccount(acct, timeout, statsDir)
		okGames += n
		if path != "" {
			lastPath = path
		}
	}

	r.Store.RunFinished(nil, okGames)
	elapsed := time.Since(startTime)
	slog.Info("========== 抓取完成 ==========", "component", "pipeline", "duration", elapsed.String(), "path", lastPath, "games", okGames)
	return nil
}

func (r *BoardRun) runAccount(acct accounts.Account, timeout int, statsDir string) (okGames int, path string) {
	r.Store.SetPhase(status.PhaseCookie)
	ctx, cancel := context.Background(), func() {}
	if r.NewContext != nil {
		ctx, cancel = r.NewContext(timeout)
	}

	cookies, err := r.prep(ctx, acct)
	if err != nil {
		slog.Error("Cookie准备失败", "component", "pipeline", "account", acct.Username, "error", err)
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", err.Error())
		cancel()
		return 0, ""
	}
	r.Store.SetCookie(cookies, auth.IsCookieValid(cookies))
	r.Store.SetLoginPhase(status.LoginIdle, "")

	r.Store.SetPhase(status.PhaseScraping)
	snap, err := r.scrape(ctx, acct, cookies)
	snap.Account = acct.Username
	cancel()
	if err != nil {
		slog.Error("抓取数据失败", "component", "pipeline", "account", acct.Username, "error", err)
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", err.Error())
		return 0, ""
	}
	recordBoardGames(r.Store, snap)
	if snap.Date != "" {
		r.Store.SetStatsDate(snap.Date)
	}

	if r.Save == nil {
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", "save function not configured")
		return 0, ""
	}
	path, saveErr := r.Save(statsDir, snap)
	if saveErr != nil {
		slog.Error("写入看板统计失败", "component", "pipeline", "account", acct.Username, "error", saveErr, "dir", statsDir)
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", saveErr.Error())
		return 0, ""
	}

	if r.SyncFeishu != nil {
		newC, updC, syncErr := r.SyncFeishu(context.Background(), snap)
		if syncErr != nil {
			slog.Error("同步飞书多维表格失败", "component", "pipeline", "account", acct.Username, "error", syncErr)
		} else {
			slog.Info("已同步飞书多维表格", "component", "pipeline", "new", newC, "updated", updC)
		}
	}
	_ = r.Accounts.UpdateStatus(acct.ID, "ok", "")
	for _, g := range snap.Games {
		if g.Error == "" {
			okGames++
		}
	}
	return okGames, path
}

func (r *BoardRun) prep(ctx context.Context, acct accounts.Account) (*models.CookieData, error) {
	if r.PrepAccountCookies != nil {
		return r.PrepAccountCookies(ctx, acct)
	}
	return nil, errors.New("cookie prep not configured")
}

func (r *BoardRun) scrape(ctx context.Context, acct accounts.Account, cookies *models.CookieData) (models.BoardStatsSnapshot, error) {
	if r.ScrapeAccount != nil {
		return r.ScrapeAccount(ctx, acct, cookies)
	}
	return models.BoardStatsSnapshot{}, errors.New("scrape not configured")
}

func recordBoardGames(st *status.Store, snap models.BoardStatsSnapshot) {
	for _, g := range snap.Games {
		var gErr error
		if g.Error != "" {
			gErr = errors.New(g.Error)
		}
		st.RecordGameStats(g.GameName, g.Metrics, gErr)
	}
}
