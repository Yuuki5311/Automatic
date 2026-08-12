package web

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

//go:embed templates/index.html
var templatesFS embed.FS

// Server Web 状态仪表盘服务器。
type Server struct {
	store        *status.Store
	tmpl         *template.Template
	srv          *http.Server
	cfg          *config.Config
	loginSvc     *auth.LoginService
	browserMgr   *browser.Manager
	captchaSolver captcha.Solver
}

// New 创建 Web 服务器。
func New(store *status.Store, cfg *config.Config, loginSvc *auth.LoginService,
	browserMgr *browser.Manager, captchaSolver captcha.Solver) (*Server, error) {
	tmpl, err := template.New("index.html").Funcs(template.FuncMap{
		"fmtTime":      fmtTime,
		"fmtDur":       fmtDur,
		"fmtRemaining": fmtRemaining,
		"remClass":     remClass,
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
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/cookies", s.handleCookies)
	mux.HandleFunc("/healthz", s.handleHealth)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	return s, nil
}

// ListenAndServe 启动 HTTP 服务。
func (s *Server) ListenAndServe(addr string) error {
	s.srv.Addr = addr
	return s.srv.ListenAndServe()
}

// Shutdown 优雅关闭。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, s.store.Snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.store.Snapshot())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// handleLogin 触发自动登录，异步执行。
// POST /api/login  — 无需请求体，使用配置文件中的账号密码。
// 登录过程中，/api/status 的 login_phase 字段会依次变为 running → success/failed。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 检查是否已有登录在进行
	snap := s.store.Snapshot()
	if snap.LoginPhase == status.LoginRunning {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running"})
		return
	}

	s.store.SetLoginPhase(status.LoginRunning, "")

	// 异步执行登录，不阻塞 HTTP 响应
	go func() {
		ctx, cancel := s.browserMgr.NewContext(s.cfg.Browser.TimeoutSec)
		defer cancel()

		cookies, err := s.loginSvc.PerformLogin(ctx, s.cfg, s.captchaSolver)
		if err != nil {
			slog.Error("UI触发登录失败", "component", "web", "error", err)
			s.store.SetLoginPhase(status.LoginFailed, err.Error())
			return
		}

		// 登录成功，保存 Cookie
		if err := auth.SaveCookies(s.cfg.JYM.CookiePath, cookies); err != nil {
			slog.Error("保存Cookie失败", "component", "web", "error", err)
		}
		s.store.SetCookie(cookies, auth.IsCookieValid(cookies))
		s.store.SetLoginPhase(status.LoginSuccess, "")
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
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
