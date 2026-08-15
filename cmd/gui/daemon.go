package main

import (
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/logger"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

// daemonCron 可热更新表达式的定时器。
type daemonCron struct {
	mu       sync.Mutex
	cron     *cron.Cron
	entryID  cron.EntryID
	scrapeFn func()
	st       *status.Store
	expr     string
}

func startDaemonCron(cfg *config.Config, st *status.Store, scrapeFn func()) (*daemonCron, error) {
	d := &daemonCron{scrapeFn: scrapeFn, st: st}
	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(logger.NewCronLogger())))
	id, err := c.AddFunc(cfg.Scraper.CronExpr, scrapeFn)
	if err != nil {
		return nil, err
	}
	d.cron = c
	d.entryID = id
	d.expr = cfg.Scraper.CronExpr
	syncScheduleStatus(st, cfg.Scraper.CronExpr)
	st.SetDaemonState(status.DaemonRunning)
	c.Start()
	return d, nil
}

func (d *daemonCron) Reschedule(expr string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cron.Remove(d.entryID)
	id, err := d.cron.AddFunc(expr, d.scrapeFn)
	if err != nil {
		// 尝试恢复旧表达式
		if oldID, oldErr := d.cron.AddFunc(d.expr, d.scrapeFn); oldErr == nil {
			d.entryID = oldID
		}
		return err
	}
	d.entryID = id
	d.expr = expr
	syncScheduleStatus(d.st, expr)
	return nil
}

func (d *daemonCron) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cron != nil {
		d.cron.Stop()
	}
}

func syncScheduleStatus(st *status.Store, expr string) {
	if st == nil {
		return
	}
	st.SetSchedule(expr, config.HHMMFromCron(expr), config.CronLabelFromExpr(expr))
}
