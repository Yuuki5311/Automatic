package main

import (
	"github.com/robfig/cron/v3"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/logger"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func startDaemonCron(cfg *config.Config, st *status.Store, scrapeFn func()) (*cron.Cron, error) {
	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(logger.NewCronLogger())))
	if _, err := c.AddFunc(cfg.Scraper.CronExpr, scrapeFn); err != nil {
		return nil, err
	}
	st.SetDaemonState(status.DaemonRunning)
	c.Start()
	return c, nil
}
