package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
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
	"github.com/example/jiaoyimao-scraper/internal/scrapehistory"
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
	History            *scrapehistory.Store

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
		err := errors.New("没有可用账户，请先在 UI 拉取列表")
		slog.Error("没有可用账户", "component", "pipeline", "error", err)
		r.Store.RunFinished(err, 0, 0, 0)
		return nil
	}

	okGames := 0
	okAccounts := 0
	skippedAccounts := 0
	lastPath := ""
	for i, acct := range accts {
		if i > 0 {
			wait := time.Duration(5+rand.Intn(6)) * time.Second // 5–10s
			slog.Info("账号间等待", "component", "pipeline", "wait", wait.String(), "next", acct.Username)
			time.Sleep(wait)
		}
		n, path, skipped := r.runAccount(acct, timeout, statsDir)
		if skipped {
			skippedAccounts++
		} else {
			okAccounts++
			okGames += n
		}
		if path != "" {
			lastPath = path
		}
	}

	var finishErr error
	if skippedAccounts > 0 && okAccounts == 0 {
		finishErr = errors.New("全部账户跳过")
	}
	r.Store.RunFinished(finishErr, okGames, okAccounts, skippedAccounts)
	elapsed := time.Since(startTime)
	slog.Info("========== 抓取完成 ==========", "component", "pipeline", "duration", elapsed.String(), "path", lastPath, "games", okGames, "ok_accounts", okAccounts, "skipped_accounts", skippedAccounts)
	return nil
}

func (r *BoardRun) runAccount(acct accounts.Account, timeout int, statsDir string) (okGames int, path string, skipped bool) {
	r.Store.SetPhase(status.PhaseCookie)
	ctx, cancel := context.Background(), func() {}
	if r.NewContext != nil {
		ctx, cancel = r.NewContext(timeout)
	}

	cookies, err := r.prep(ctx, acct)
	if err != nil {
		slog.Error("Cookie准备失败", "component", "pipeline", "account", acct.Username, "error", err)
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", err.Error())
		if isLoginPageFailure(err) {
			_ = r.Accounts.SetEnabled(acct.ID, false, "自动登录后仍在登录页，已禁用")
		}
		r.recordHistory(acct.Username, "skipped", err.Error())
		cancel()
		return 0, "", true
	}
	r.Store.SetCookie(cookies, auth.IsCookieValid(cookies))
	r.Store.SetLoginPhase(status.LoginIdle, "")

	r.Store.SetPhase(status.PhaseScraping)
	snap, err := r.scrape(ctx, acct, cookies)
	snap.Account = acct.Username
	snap.UID = auth.MemberUID(cookies)
	cancel()
	if err != nil {
		slog.Error("抓取数据失败", "component", "pipeline", "account", acct.Username, "error", err)
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", err.Error())
		r.recordHistory(acct.Username, "skipped", err.Error())
		return 0, "", true
	}
	recordBoardGames(r.Store, snap)
	if snap.Date != "" {
		r.Store.SetStatsDate(snap.Date)
	}

	if r.Save == nil {
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", "save function not configured")
		r.recordHistory(acct.Username, "skipped", "save function not configured")
		return 0, "", true
	}
	path, saveErr := r.Save(statsDir, snap)
	if saveErr != nil {
		slog.Error("写入看板统计失败", "component", "pipeline", "account", acct.Username, "error", saveErr, "dir", statsDir)
		_ = r.Accounts.UpdateStatus(acct.ID, "skipped", saveErr.Error())
		r.recordHistory(acct.Username, "skipped", saveErr.Error())
		return 0, "", true
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
	r.recordHistory(acct.Username, "ok", "")
	for _, g := range snap.Games {
		if g.Error == "" {
			okGames++
		}
	}
	return okGames, path, false
}

func (r *BoardRun) recordHistory(account, st, errMsg string) {
	if r == nil || r.History == nil {
		return
	}
	if err := r.History.Append(account, st, errMsg); err != nil {
		slog.Warn("写入抓取历史失败", "component", "pipeline", "account", account, "error", err)
	}
	r.syncHistoryToStatus()
}

func (r *BoardRun) syncHistoryToStatus() {
	if r == nil || r.Store == nil || r.History == nil {
		return
	}
	list := r.History.List()
	out := make([]status.HistoryEntry, len(list))
	for i, e := range list {
		out[i] = status.HistoryEntry{
			ID: e.ID, At: e.At, Account: e.Account, Status: e.Status, Error: e.Error,
		}
	}
	r.Store.SetScrapeHistory(out)
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

func isLoginPageFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "登录页") || strings.Contains(strings.ToLower(msg), "still on login")
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
