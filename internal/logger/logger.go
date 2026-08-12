package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config 日志配置。
type Config struct {
	Level      string // "debug"|"info"|"warn"|"error"
	File       string // 日志文件路径，空则仅控制台
	MaxSizeMB  int    // 单文件轮转阈值
	MaxBackups int    // 保留备份数
}

// Init 初始化全局 slog logger，返回关闭函数。
// 控制台使用 TextHandler，文件使用 JSONHandler，两者由同一 level 控制。
func Init(cfg Config) (cleanup func(), err error) {
	lvl := parseLevel(cfg.Level)
	handlers := []slog.Handler{
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}),
	}

	var rotWriter *rotatingWriter
	if cfg.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.File), 0755); err != nil {
			return nil, fmt.Errorf("创建日志目录失败: %w", err)
		}
		rotWriter = &rotatingWriter{
			path:       cfg.File,
			maxSize:    int64(cfg.MaxSizeMB) * 1024 * 1024,
			maxBackups: cfg.MaxBackups,
		}
		handlers = append(handlers, slog.NewJSONHandler(rotWriter, &slog.HandlerOptions{Level: lvl}))
	}

	slog.SetDefault(slog.New(newMultiHandler(handlers...)))
	return func() {
		if rotWriter != nil {
			rotWriter.Close()
		}
	}, nil
}

// NewCronLogger 返回适配 robfig/cron 的 cron.Logger 接口实现。
func NewCronLogger() *cronLogger {
	return &cronLogger{}
}

// cronLogger 将 slog 适配为 cron.Logger（Info + Error 方法）。
type cronLogger struct{}

func (l *cronLogger) Info(msg string, keysAndValues ...interface{}) {
	slog.Info(msg, keysAndValues...)
}

func (l *cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	args := append([]interface{}{"error", err}, keysAndValues...)
	slog.Error(msg, args...)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// multiHandler 将记录扇出到多个子 handler。
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, s := range h.handlers {
		if s.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, s := range h.handlers {
		if s.Enabled(ctx, r.Level) {
			// Clone record so each handler gets its own copy
			r2 := r.Clone()
			if err := s.Handle(ctx, r2); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clones := make([]slog.Handler, len(h.handlers))
	for i, s := range h.handlers {
		clones[i] = s.WithAttrs(attrs)
	}
	return newMultiHandler(clones...)
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	clones := make([]slog.Handler, len(h.handlers))
	for i, s := range h.handlers {
		clones[i] = s.WithGroup(name)
	}
	return newMultiHandler(clones...)
}

// rotatingWriter 实现带大小和日期轮转的 io.WriteCloser。
type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	maxBackups int

	file   *os.File
	size   int64
	curDay string
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if w.file == nil || w.curDay != today {
		if err := w.rotate(today); err != nil {
			return 0, err
		}
	}
	if w.maxSize > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateSize(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func (w *rotatingWriter) rotate(today string) error {
	if w.file != nil {
		w.file.Close()
		// 按日期重命名旧文件
		backup := w.path + "." + w.curDay
		os.Rename(w.path, backup)
	}
	w.curDay = today
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.file = f
	fi, _ := f.Stat()
	w.size = fi.Size()
	w.prune()
	return nil
}

func (w *rotatingWriter) rotateSize() error {
	if w.file != nil {
		w.file.Close()
	}
	// 找下一个可用编号
	for i := 1; i < 1000; i++ {
		backup := fmt.Sprintf("%s.%d", w.path, i)
		if _, err := os.Stat(backup); os.IsNotExist(err) {
			os.Rename(w.path, backup)
			break
		}
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	w.prune()
	return nil
}

func (w *rotatingWriter) prune() {
	if w.maxBackups <= 0 {
		return
	}
	dir := filepath.Dir(w.path)
	base := filepath.Base(w.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var backups []os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base+".") {
			backups = append(backups, e)
		}
	}
	if len(backups) <= w.maxBackups {
		return
	}
	// 按修改时间排序，删除最旧的
	sort.Slice(backups, func(i, j int) bool {
		ii, _ := backups[i].Info()
		jj, _ := backups[j].Info()
		if ii == nil || jj == nil {
			return false
		}
		return ii.ModTime().Before(jj.ModTime())
	})
	for i := 0; i < len(backups)-w.maxBackups; i++ {
		os.Remove(filepath.Join(dir, backups[i].Name()))
	}
}

// Ensure io.Writer compliance.
var _ io.WriteCloser = (*rotatingWriter)(nil)
