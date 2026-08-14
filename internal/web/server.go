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
	"sync"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/leyoo"
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
	leyoo         *leyoo.Client
	scrapeFn      func()
	scrapeMu      sync.Mutex
	scraping      bool
	pullMu        sync.Mutex
	pulling       bool
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
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/accounts", s.handleAccountsList)
	mux.HandleFunc("POST /api/accounts", s.handleAccountsAddRemoved)
	mux.HandleFunc("POST /api/accounts/pull", s.handleAccountsPull)
	mux.HandleFunc("POST /api/accounts/{id}/enable", s.handleAccountEnable)
	mux.HandleFunc("POST /api/accounts/{id}/disable", s.handleAccountDisable)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleAccountDelete)
	mux.HandleFunc("POST /api/accounts/{id}/cookies", s.handleAccountCookies)
	mux.HandleFunc("POST /api/accounts/{id}/login", s.handleAccountLogin)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.refreshAccountStatus()

	return s, nil
}

// SetScrapeFunc 注入与主流程相同的看板抓取回调（由 cmd/scraper 或 gui 注入）。
func (s *Server) SetScrapeFunc(fn func()) {
	s.scrapeMu.Lock()
	defer s.scrapeMu.Unlock()
	s.scrapeFn = fn
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
		list, err := client.ListCatBySupplier(req.SupplierID)
		if err != nil {
			slog.Error("拉取店铺列表失败", "component", "web", "error", err)
			if s.store != nil {
				s.store.SetPullPhase(status.PullFailed, err.Error(), 0)
			}
			return
		}
		for _, remote := range list {
			acct, _, upsertErr := s.accounts.UpsertFromRemote(
				remote.Mobile, remote.ThirdPassword, remote.Name,
				remote.ID, remote.SupplierID, remote.PlatformKey,
			)
			if upsertErr != nil {
				slog.Warn("同步店铺失败", "component", "web", "mobile", remote.Mobile, "error", upsertErr)
				continue
			}
			if err := auth.ImportFromHeader(acct.CookiePath, remote.Cookie, ".jiaoyimao.com"); err != nil {
				_ = s.accounts.SetEnabled(acct.ID, false, "Cookie 导入失败: "+err.Error())
				continue
			}
			cookies, _ := auth.LoadCookies(acct.CookiePath)
			if !auth.IsCookieValid(cookies) {
				_ = s.accounts.SetEnabled(acct.ID, false, "Cookie 无效")
			}
		}
		s.refreshAccountStatus()
		if s.store != nil {
			s.store.SetPullPhase(status.PullSuccess, "", len(list))
		}
		slog.Info("店铺列表已同步", "component", "web", "supplier_id", req.SupplierID, "count", len(list))
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
