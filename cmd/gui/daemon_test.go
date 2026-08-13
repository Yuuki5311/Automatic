package main

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/status"
	"github.com/example/jiaoyimao-scraper/internal/web"
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

func TestStartLocalDashboardServesOnKeptListener(t *testing.T) {
	store := status.NewStore()
	srv, err := web.New(store, &config.Config{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := startLocalDashboard(srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	client := &http.Client{Timeout: 2 * time.Second}
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err = client.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("healthz via kept listener: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("body=%q", b)
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
