package web

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/accountsync"
	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/feishu"
	"github.com/example/jiaoyimao-scraper/internal/leyoo"
	"github.com/example/jiaoyimao-scraper/internal/scrapehistory"
	"github.com/example/jiaoyimao-scraper/internal/scraper"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

//go:embed templates/index.html
var templatesFS embed.FS

// Server Web 状态仪表盘服务器。
type Server struct {
	store         *status.Store
	tmpl          *template.Template
	srv           *http.Server
	cfg           *config.Config
	loginSvc      *auth.LoginService
	browserMgr    *browser.Manager
	captchaSolver captcha.Solver
	accounts      *accounts.Store
	history       *scrapehistory.Store
	leyoo         *leyoo.Client
	scrapeFn      func()
	scrapeMu      sync.Mutex
	scraping      bool
	pullMu        sync.Mutex
	pulling       bool
	configPath    string
	rescheduleFn  func(expr string) error
}

// New 创建 Web 服务器。
func New(store *status.Store, cfg *config.Config, loginSvc *auth.LoginService,
	browserMgr *browser.Manager, captchaSolver captcha.Solver, acctStore *accounts.Store) (*Server, error) {
	tmpl, err := template.New("index.html").Funcs(template.FuncMap{
		"fmtTime":      fmtTime,
		"fmtDur":       fmtDur,
		"fmtRemaining": fmtRemaining,
		"remClass":     remClass,
		"groupHistory": groupHistory,
	}).ParseFS(templatesFS, "templates/index.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		store:         store,
		tmpl:          tmpl,
		cfg:           cfg,
		loginSvc:      loginSvc,
		browserMgr:    browserMgr,
		captchaSolver: captchaSolver,
		accounts:      acctStore,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/cookies", s.handleCookies)
	mux.HandleFunc("/api/scrape", s.handleScrape)
	mux.HandleFunc("/api/schedule", s.handleSchedule)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/accounts", s.handleAccountsList)
	mux.HandleFunc("POST /api/accounts", s.handleAccountsAddRemoved)
	mux.HandleFunc("POST /api/accounts/pull", s.handleAccountsPull)
	mux.HandleFunc("POST /api/accounts/{id}/enable", s.handleAccountEnable)
	mux.HandleFunc("POST /api/accounts/{id}/disable", s.handleAccountDisable)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleAccountDelete)
	mux.HandleFunc("POST /api/accounts/{id}/cookies", s.handleAccountCookies)
	mux.HandleFunc("POST /api/accounts/{id}/login", s.handleAccountLogin)
	mux.HandleFunc("GET /api/board/games", s.handleBoardGames)
	mux.HandleFunc("GET /api/board/metric-fields", s.handleBoardMetricFields)
	mux.HandleFunc("POST /api/board/manual", s.handleBoardManual)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.refreshAccountStatus()
	s.SyncScheduleFromConfig()

	return s, nil
}

// SetScrapeFunc 注入与主流程相同的看板抓取回调（由 cmd/scraper 或 gui 注入）。
func (s *Server) SetScrapeFunc(fn func()) {
	s.scrapeMu.Lock()
	defer s.scrapeMu.Unlock()
	s.scrapeFn = fn
}

// SetConfigPath 设置可写回的配置文件路径（用于保存定时时间）。
func (s *Server) SetConfigPath(path string) {
	s.configPath = path
}

// SetRescheduleFunc 注入 cron 热更新回调。
func (s *Server) SetRescheduleFunc(fn func(expr string) error) {
	s.rescheduleFn = fn
}

// SetHistoryStore 注入抓取历史（手工补充成功后追加记录）。
func (s *Server) SetHistoryStore(h *scrapehistory.Store) {
	s.history = h
}

// SyncScheduleFromConfig 把当前配置中的 cron 同步到 status（供 UI 展示）。
func (s *Server) SyncScheduleFromConfig() {
	if s.cfg == nil || s.store == nil {
		return
	}
	expr := s.cfg.Scraper.CronExpr
	s.store.SetSchedule(expr, config.HHMMFromCron(expr), config.CronLabelFromExpr(expr))
}

// ListenAndServe 启动 HTTP 服务。
func (s *Server) ListenAndServe(addr string) error {
	s.srv.Addr = addr
	return s.srv.ListenAndServe()
}

// Serve 在已绑定的 listener 上提供服务（避免 Close 后再 ListenAndServe 的端口竞态）。
func (s *Server) Serve(l net.Listener) error {
	return s.srv.Serve(l)
}

// Shutdown 优雅关闭。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	s.refreshAccountStatus()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, s.store.Snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.refreshAccountStatus()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.store.Snapshot())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleBoardGames(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"games": scraper.BoardGameNames()})
}

func (s *Server) handleBoardMetricFields(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"fields": feishu.ManualBoardMetricFields()})
}

func (s *Server) handleBoardManual(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil || s.cfg.Feishu.AppID == "" || s.cfg.Feishu.AppSecret == "" ||
		s.cfg.Feishu.BitableID == "" || s.cfg.Feishu.BoardTableID == "" ||
		strings.Contains(s.cfg.Feishu.AppID, "xxxx") {
		http.Error(w, "飞书未配置", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Date     string              `json:"date"`
		Account  string              `json:"account"`
		Game     string              `json:"game"`
		Metrics  map[string]*float64 `json:"metrics"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON 解析失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Date = strings.TrimSpace(req.Date)
	req.Account = strings.TrimSpace(req.Account)
	req.Game = strings.TrimSpace(req.Game)
	if req.Date == "" || req.Account == "" || req.Game == "" {
		http.Error(w, "日期、账号、游戏均不能为空", http.StatusBadRequest)
		return
	}
	if _, err := time.ParseInLocation("2006-01-02", req.Date, time.Local); err != nil {
		http.Error(w, "日期格式须为 YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	knownGames := map[string]struct{}{}
	for _, g := range scraper.BoardGameNames() {
		knownGames[g] = struct{}{}
	}
	if _, ok := knownGames[req.Game]; !ok {
		http.Error(w, "未知游戏: "+req.Game, http.StatusBadRequest)
		return
	}
	allowed := map[string]struct{}{}
	for _, f := range feishu.ManualBoardMetricFields() {
		allowed[f] = struct{}{}
	}
	for k := range req.Metrics {
		if _, ok := allowed[k]; !ok {
			http.Error(w, "未知指标字段: "+k, http.StatusBadRequest)
			return
		}
	}

	shopName, uid := "", ""
	if s.accounts != nil {
		if acct, ok := s.accounts.FindByUsername(req.Account); ok {
			shopName = acct.ShopName
			cookies, _ := auth.LoadCookies(acct.CookiePath)
			uid = auth.MemberUID(cookies)
		} else {
			http.Error(w, "账户不存在", http.StatusNotFound)
			return
		}
	}

	client := feishu.NewClient(&s.cfg.Feishu)
	ops := feishu.NewBitableOps(client, s.cfg.Feishu.BitableID)
	action, err := ops.UpsertBoardManualRow(
		r.Context(), s.cfg.Feishu.BoardTableID,
		req.Date, req.Account, shopName, uid, req.Game, req.Metrics, time.Now(),
	)
	if err != nil {
		slog.Error("手工补充飞书失败", "component", "web", "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if s.history != nil {
		msg := "手工补充 " + req.Game
		if err := s.history.Append(req.Account, "manual", msg); err != nil {
			slog.Warn("写入手工补充历史失败", "component", "web", "error", err)
		} else {
			s.syncHistoryToStatus()
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": action})
}

func (s *Server) syncHistoryToStatus() {
	if s == nil || s.store == nil || s.history == nil {
		return
	}
	list := s.history.List()
	out := make([]status.HistoryEntry, len(list))
	for i, e := range list {
		out[i] = status.HistoryEntry{
			ID: e.ID, At: e.At, Account: e.Account, Status: e.Status, Error: e.Error,
		}
	}
	s.store.SetScrapeHistory(out)
}

// handleScrape 异步触发看板抓取。
// POST /api/scrape
func (s *Server) handleScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.scrapeMu.Lock()
	fn := s.scrapeFn
	if fn == nil {
		s.scrapeMu.Unlock()
		http.Error(w, "scrape not configured", http.StatusServiceUnavailable)
		return
	}

	snap := s.store.Snapshot()
	if s.scraping || snap.CurrentRun != nil || snap.Phase == status.PhaseScraping || snap.Phase == status.PhaseCookie {
		s.scrapeMu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running"})
		return
	}
	s.scraping = true
	s.scrapeMu.Unlock()

	go func() {
		defer func() {
			s.scrapeMu.Lock()
			s.scraping = false
			s.scrapeMu.Unlock()
		}()
		fn()
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// handleSchedule GET 当前定时；POST {"time":"14:30"} 设置为每天该时刻。
func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch r.Method {
	case http.MethodGet:
		snap := s.store.Snapshot()
		json.NewEncoder(w).Encode(map[string]string{
			"time":  snap.ScheduleTime,
			"label": snap.ScheduleLabel,
			"cron":  snap.CronExpr,
		})
	case http.MethodPost:
		var req struct {
			Time string `json:"time"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		hour, minute, err := config.ParseHHMM(req.Time)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		expr, err := config.DailyCronExpr(hour, minute)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if s.rescheduleFn != nil {
			if err := s.rescheduleFn(expr); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "定时表达式无效: " + err.Error()})
				return
			}
		}
		if s.cfg != nil {
			s.cfg.Scraper.CronExpr = expr
		}
		if s.configPath != "" {
			if err := config.SaveCronExpr(s.configPath, expr); err != nil {
				slog.Warn("保存 cron_expr 失败", "component", "web", "path", s.configPath, "error", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "定时已生效但写入配置失败: " + err.Error()})
				return
			}
		}
		label := config.FormatDailyLabel(hour, minute)
		hhmm := config.HHMMFromCron(expr)
		if s.store != nil {
			s.store.SetSchedule(expr, hhmm, label)
		}
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"time":   hhmm,
			"label":  label,
			"cron":   expr,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLogin 触发自动登录，异步执行。
// POST /api/login — 仅当账户库恰好有一个账号时登录该账号；否则 400，请用 POST /api/accounts/{id}/login。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.accounts == nil || len(s.accounts.List()) != 1 {
		http.Error(w, "use POST /api/accounts/{id}/login for a specific account", http.StatusBadRequest)
		return
	}
	s.startAccountLogin(s.accounts.List()[0], w)
}

// handleCookies 手动导入 Cookie。
// POST /api/cookies  — 请求体为 EditThisCookie 格式的 JSON 数组。
func (s *Server) handleCookies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var rawCookies []map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&rawCookies); err != nil {
		http.Error(w, "JSON 解析失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(rawCookies) == 0 {
		http.Error(w, "Cookie 列表为空", http.StatusBadRequest)
		return
	}

	// 重新序列化为 JSON，走 auth.ImportFromJSON 的标准导入路径
	jsonBytes, err := json.Marshal(rawCookies)
	if err != nil {
		http.Error(w, "序列化失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := auth.ImportFromJSON(s.cfg.JYM.CookiePath, jsonBytes); err != nil {
		slog.Error("手动导入Cookie失败", "component", "web", "error", err)
		http.Error(w, "导入失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 导入成功后立即更新状态
	cookies, err := auth.LoadCookies(s.cfg.JYM.CookiePath)
	if err == nil {
		s.store.SetCookie(cookies, auth.IsCookieValid(cookies))
	}

	slog.Info("Cookie已通过UI手动导入", "component", "web", "count", len(rawCookies))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"count":  len(rawCookies),
	})
}

func (s *Server) handleAccountsList(w http.ResponseWriter, _ *http.Request) {
	s.refreshAccountStatus()
	list := s.accountStatuses()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(list)
}

func (s *Server) handleAccountsAddRemoved(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "手动添加已移除，请使用 POST /api/accounts/pull 拉取列表", http.StatusMethodNotAllowed)
}

func (s *Server) handleAccountsPull(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		http.Error(w, "accounts not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		SupplierID int `json:"supplier_id"`
	}
	if r.Body != nil && r.Body != http.NoBody {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			http.Error(w, "JSON 解析失败: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.SupplierID <= 0 {
		req.SupplierID = 1
	}

	s.pullMu.Lock()
	if s.pulling {
		s.pullMu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running"})
		return
	}
	s.pulling = true
	s.pullMu.Unlock()
	if s.store != nil {
		s.store.SetPullPhase(status.PullRunning, "", 0)
	}

	go func() {
		defer func() {
			s.pullMu.Lock()
			s.pulling = false
			s.pullMu.Unlock()
		}()
		client := s.leyoo
		if client == nil {
			client = leyoo.NewClient("")
		}
		crypto := accountsync.CryptoFromConfig(s.cfg)
		if crypto == nil {
			slog.Warn("未配置 AES 密钥，third_account 将无法解密（设 credential.aes_key 或环境变量 THIRDPARTYSYNC_AES_KEY）", "component", "web")
		}
		kept, err := accountsync.SyncCatBySupplier(s.accounts, client, crypto, req.SupplierID)
		if err != nil {
			slog.Error("拉取店铺列表失败", "component", "web", "error", err)
			if s.store != nil {
				s.store.SetPullPhase(status.PullFailed, err.Error(), 0)
			}
			return
		}
		s.refreshAccountStatus()
		if s.store != nil {
			s.store.SetPullPhase(status.PullSuccess, "", kept)
		}
		slog.Info("店铺列表已同步", "component", "web", "supplier_id", req.SupplierID, "kept", kept)
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (s *Server) handleAccountEnable(w http.ResponseWriter, r *http.Request) {
	s.setAccountEnabled(w, r, true)
}

func (s *Server) handleAccountDisable(w http.ResponseWriter, r *http.Request) {
	s.setAccountEnabled(w, r, false)
}

func (s *Server) setAccountEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	acct, ok := s.lookupAccount(r)
	if !ok {
		http.Error(w, "账户不存在", http.StatusNotFound)
		return
	}
	reason := ""
	if !enabled {
		reason = "手动禁用"
	}
	if err := s.accounts.SetEnabled(acct.ID, enabled, reason); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.refreshAccountStatus()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(accountToStatus(mustGetAccount(s.accounts, acct.ID)))
}

func mustGetAccount(store *accounts.Store, id string) accounts.Account {
	a, _ := store.Get(id)
	return a
}

func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.lookupAccount(r)
	if !ok {
		http.Error(w, "账户不存在", http.StatusNotFound)
		return
	}
	if err := s.accounts.Delete(acct.ID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.refreshAccountStatus()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAccountCookies(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.lookupAccount(r)
	if !ok {
		http.Error(w, "账户不存在", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := auth.ImportFromJSON(acct.CookiePath, body); err != nil {
		http.Error(w, "导入失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.refreshAccountStatus()
	cookies, _ := auth.LoadCookies(acct.CookiePath)
	count := 0
	if cookies != nil {
		count = len(cookies.Cookies)
	}
	slog.Info("Cookie已通过UI按账户导入", "component", "web", "account", acct.Username, "count", count)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"count":  count,
	})
}

func (s *Server) handleAccountLogin(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.lookupAccount(r)
	if !ok {
		http.Error(w, "账户不存在", http.StatusNotFound)
		return
	}
	s.startAccountLogin(acct, w)
}

func (s *Server) lookupAccount(r *http.Request) (accounts.Account, bool) {
	if s.accounts == nil {
		return accounts.Account{}, false
	}
	return s.accounts.Get(r.PathValue("id"))
}

func (s *Server) startAccountLogin(acct accounts.Account, w http.ResponseWriter) {
	snap := s.store.Snapshot()
	if snap.LoginPhase == status.LoginRunning {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running"})
		return
	}

	s.store.SetLoginPhase(status.LoginRunning, "")

	go func() {
		if s.browserMgr == nil || s.loginSvc == nil {
			s.store.SetLoginPhase(status.LoginFailed, "login not configured")
			s.refreshAccountStatus()
			return
		}
		ctx, cancel := s.browserMgr.NewContext(s.cfg.Browser.TimeoutSec)
		defer cancel()

		acctCfg := s.cfg.WithJYMAccount(acct.Username, acct.Password, acct.CookiePath)
		cookies, err := s.loginSvc.PerformLogin(ctx, acctCfg, s.captchaSolver)
		if err != nil {
			slog.Error("UI触发登录失败", "component", "web", "account", acct.Username, "error", err)
			s.store.SetLoginPhase(status.LoginFailed, err.Error())
			s.refreshAccountStatus()
			return
		}

		if err := auth.SaveCookies(acct.CookiePath, cookies); err != nil {
			slog.Error("保存Cookie失败", "component", "web", "account", acct.Username, "error", err)
		}
		s.store.SetCookie(cookies, auth.IsCookieValid(cookies))
		s.store.SetLoginPhase(status.LoginSuccess, "")
		s.refreshAccountStatus()
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (s *Server) refreshAccountStatus() {
	if s.store == nil || s.accounts == nil {
		return
	}
	list := s.accounts.List()
	out := make([]status.AccountStatus, 0, len(list))
	for _, a := range list {
		out = append(out, accountToStatus(a))
	}
	s.store.SetAccounts(out)
}

func (s *Server) accountStatuses() []status.AccountStatus {
	if s.store == nil {
		return []status.AccountStatus{}
	}
	list := s.store.Snapshot().Accounts
	if list == nil {
		return []status.AccountStatus{}
	}
	return list
}

func accountToStatus(a accounts.Account) status.AccountStatus {
	cookies, _ := auth.LoadCookies(a.CookiePath)
	return status.AccountStatus{
		ID:          a.ID,
		Username:    a.Username,
		ShopName:    a.ShopName,
		Enabled:     a.Enabled,
		LastStatus:  a.LastStatus,
		LastError:   a.LastError,
		LastRunAt:   a.LastRunAt,
		CookieValid: auth.IsCookieValid(cookies),
		HasPassword: a.Password != "",
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

func fmtDur(secs float64) string {
	if secs <= 0 {
		return "-"
	}
	d := time.Duration(secs) * time.Second
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	return d.Truncate(time.Second).String()
}

func fmtRemaining(secs int64) string {
	if secs <= 0 {
		return "已过期"
	}
	d := time.Duration(secs) * time.Second
	if d >= 24*time.Hour {
		return d.Truncate(time.Hour).String()
	}
	if d >= time.Hour {
		return d.Truncate(time.Minute).String()
	}
	return d.Truncate(time.Second).String()
}

func remClass(secs int64) string {
	if secs <= 0 {
		return "expired"
	}
	if secs < 3600 {
		return "critical"
	}
	if secs < 86400 {
		return "warn"
	}
	return "ok"
}

type historyDayGroup struct {
	Label string
	Date  string
	Items []status.HistoryEntry
}

func groupHistory(entries []status.HistoryEntry) []historyDayGroup {
	if len(entries) == 0 {
		return nil
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	order := make([]string, 0)
	buckets := map[string][]status.HistoryEntry{}
	for _, e := range entries {
		d := e.At.In(now.Location()).Format("2006-01-02")
		if _, ok := buckets[d]; !ok {
			order = append(order, d)
		}
		buckets[d] = append(buckets[d], e)
	}
	out := make([]historyDayGroup, 0, len(order))
	for _, d := range order {
		label := d
		switch d {
		case today:
			label = "今天 (" + d + ")"
		case yesterday:
			label = "昨天 (" + d + ")"
		}
		out = append(out, historyDayGroup{Label: label, Date: d, Items: buckets[d]})
	}
	return out
}
