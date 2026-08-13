package main

import (
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestStartDaemonCron_SetsRunningAndAcceptsDailyNine(t *testing.T) {
	st := status.NewStore()
	cfg := &config.Config{Scraper: config.ScraperConfig{CronExpr: "0 9 * * *"}}
	called := false
	c, err := startDaemonCron(cfg, st, func() { called = true })
	if err != nil {
		t.Fatalf("startDaemonCron: %v", err)
	}
	t.Cleanup(func() { c.Stop() })

	if got := st.Snapshot().DaemonState; got != status.DaemonRunning {
		t.Fatalf("DaemonState = %q, want %q", got, status.DaemonRunning)
	}
	if called {
		t.Fatal("cron must not scrape on start")
	}
}

func TestStartDaemonCron_RejectsInvalidExpr(t *testing.T) {
	st := status.NewStore()
	cfg := &config.Config{Scraper: config.ScraperConfig{CronExpr: "not-a-cron-expr"}}
	_, err := startDaemonCron(cfg, st, func() {})
	if err == nil {
		t.Fatal("expected invalid cron error")
	}
}
