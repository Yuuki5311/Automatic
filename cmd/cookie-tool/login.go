package main

import (
	"context"
	"fmt"

	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

func newCaptchaSolver(cfg *config.CaptchaConfig) captcha.Solver {
	if cfg == nil {
		return captcha.NewSliderSolver(&config.CaptchaConfig{MaxRetry: 3})
	}
	switch cfg.Provider {
	case "chaojiying", "2captcha":
		return captcha.NewThirdPartySolver(cfg)
	default:
		return captcha.NewSliderSolver(cfg)
	}
}

func performLoginAndSave(
	ctx context.Context,
	cfg *config.Config,
	solver captcha.Solver,
	perform func(context.Context, *config.Config, captcha.Solver) (*models.CookieData, error),
	save func(string, *models.CookieData) error,
) error {
	cookies, err := perform(ctx, cfg, solver)
	if err != nil {
		return fmt.Errorf("自动登录失败: %w", err)
	}
	if err := save(cfg.JYM.CookiePath, cookies); err != nil {
		return fmt.Errorf("保存Cookie失败: %w", err)
	}
	return nil
}
