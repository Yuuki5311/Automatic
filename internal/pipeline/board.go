package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
	"github.com/example/jiaoyimao-scraper/internal/scraper"
	"github.com/example/jiaoyimao-scraper/internal/statsstore"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

// BoardRun 看板抓取编排（Cookie → ScrapeBoardAll → 落盘 → 更新 status）。
// 由 cmd/scraper 与 Web /api/scrape 共用，避免复制 runScrape。
type BoardRun struct {
	Cfg         *config.Config
	Store       *status.Store
	NewContext  func(timeoutSec int) (context.Context, context.CancelFunc)
	PrepCookies func(ctx context.Context) (*models.CookieData, error)
	Scrape      func(ctx context.Context, cookies *models.CookieData) (models.BoardStatsSnapshot, error)
	Save        func(dir string, snap models.BoardStatsSnapshot) (string, error)
}

// NewBoardRun 组装真实依赖的看板抓取流程。
func NewBoardRun(cfg *config.Config, browserMgr *browser.Manager, loginSvc *auth.LoginService, solver captcha.Solver, st *status.Store) *BoardRun {
	return &BoardRun{
		Cfg:   cfg,
		Store: st,
		NewContext: func(timeoutSec int) (context.Context, context.CancelFunc) {
			return browserMgr.NewContext(timeoutSec)
		},
		PrepCookies: func(ctx context.Context) (*models.CookieData, error) {
			return loginSvc.RefreshIfNeeded(ctx, cfg, solver)
		},
		Scrape: func(ctx context.Context, cookies *models.CookieData) (models.BoardStatsSnapshot, error) {
			mgr := scraper.NewManager(cfg, browserMgr, cookies)
			mgr.SetSessionRefresher(func(refreshCtx context.Context) (*models.CookieData, error) {
				newC, refreshErr := loginSvc.RefreshIfNeeded(refreshCtx, cfg, solver)
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
}

// Run 执行一轮看板抓取。
func (r *BoardRun) Run() {
	slog.Info("========== 开始抓取 ==========", "component", "pipeline")
	startTime := time.Now()

	r.Store.RunStarted()
	r.Store.SetPhase(status.PhaseCookie)

	timeout := 0
	statsDir := ""
	if r.Cfg != nil {
		timeout = r.Cfg.Browser.TimeoutSec
		statsDir = r.Cfg.Scraper.StatsDir
	}

	ctx, cancel := context.Background(), func() {}
	if r.NewContext != nil {
		ctx, cancel = r.NewContext(timeout)
	}
	defer cancel()

	cookies, err := r.PrepCookies(ctx)
	if err != nil {
		slog.Error("Cookie准备失败", "component", "pipeline", "error", err)
		r.Store.SetLoginPhase(status.LoginFailed, err.Error())
		r.Store.RunFinished(err, 0)
		return
	}
	r.Store.SetCookie(cookies, auth.IsCookieValid(cookies))
	r.Store.SetLoginPhase(status.LoginIdle, "")

	r.Store.SetPhase(status.PhaseScraping)

	snap, err := r.Scrape(ctx, cookies)
	recordBoardGames(r.Store, snap)
	if snap.Date != "" {
		r.Store.SetStatsDate(snap.Date)
	}
	if err != nil {
		slog.Error("抓取数据失败", "component", "pipeline", "error", err)
		r.Store.RunFinished(err, 0)
		return
	}

	if r.Save == nil {
		saveErr := errors.New("save function not configured")
		r.Store.RunFinished(saveErr, 0)
		return
	}
	path, saveErr := r.Save(statsDir, snap)
	if saveErr != nil {
		slog.Error("写入看板统计失败", "component", "pipeline", "error", saveErr, "dir", statsDir)
		r.Store.RunFinished(saveErr, 0)
		return
	}

	okGames := 0
	for _, g := range snap.Games {
		if g.Error == "" {
			okGames++
		}
	}
	r.Store.RunFinished(nil, okGames)

	elapsed := time.Since(startTime)
	slog.Info("========== 抓取完成 ==========", "component", "pipeline", "duration", elapsed.String(), "path", path, "games", okGames)
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
