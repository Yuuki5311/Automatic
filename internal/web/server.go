package web

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/status"
)

//go:embed templates/index.html
var templatesFS embed.FS

// Server Web 状态仪表盘服务器。
type Server struct {
	store  *status.Store
	tmpl   *template.Template
	srv    *http.Server
}

// New 创建 Web 服务器。
func New(store *status.Store) (*Server, error) {
	tmpl, err := template.New("index.html").Funcs(template.FuncMap{
		"fmtTime": fmtTime,
		"fmtDur":  fmtDur,
		"fmtRemaining": fmtRemaining,
		"remClass": remClass,
	}).ParseFS(templatesFS, "templates/index.html")
	if err != nil {
		return nil, err
	}

	s := &Server{store: store, tmpl: tmpl}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
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
