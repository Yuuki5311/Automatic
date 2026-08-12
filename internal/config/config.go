package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	JYM     JYMConfig     `yaml:"jiaoyimao"`
	Feishu  FeishuConfig  `yaml:"feishu"`
	Browser BrowserConfig `yaml:"browser"`
	Scraper ScraperConfig `yaml:"scraper"`
	Captcha CaptchaConfig `yaml:"captcha"`
}

type JYMConfig struct {
	BaseURL    string `yaml:"base_url"` // https://merchant.jiaoyimao.com
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	CookiePath string `yaml:"cookie_path"` // Cookie 持久化路径
	LoginType  string `yaml:"login_type"`  // "password" | "sms"（短信验证码登录）
}

type FeishuConfig struct {
	AppID        string            `yaml:"app_id"`
	AppSecret    string            `yaml:"app_secret"`
	BitableID    string            `yaml:"bitable_id"`
	TableMapping map[string]string `yaml:"table_mapping"` // 游戏名 → 飞书表格ID
}

type BrowserConfig struct {
	Headless   bool   `yaml:"headless"`
	ChromePath string `yaml:"chrome_path"`
	TimeoutSec int    `yaml:"timeout_sec"`
	DebugPort  int    `yaml:"debug_port"`
}

type ScraperConfig struct {
	Mode     string       `yaml:"mode"` // "api" | "browser" | "auto"
	Games    []GameConfig `yaml:"games"`
	CronExpr string       `yaml:"cron_expr"` // 定时抓取表达式
}

type GameConfig struct {
	Name       string `yaml:"name"`        // 游戏名称
	URL        string `yaml:"url"`         // 回收页面相对路径
	TableCount int    `yaml:"table_count"` // 该游戏下的表格数量
}

type CaptchaConfig struct {
	Provider string `yaml:"provider"`  // "opencv" | "chaojiying" | "2captcha"
	APIKey   string `yaml:"api_key"`   // 第三方打码平台密钥
	MaxRetry int    `yaml:"max_retry"` // 验证码最大重试次数
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// 设置默认值
	if cfg.Browser.TimeoutSec == 0 {
		cfg.Browser.TimeoutSec = 60
	}
	if cfg.Scraper.Mode == "" {
		cfg.Scraper.Mode = "auto"
	}
	if cfg.Captcha.MaxRetry == 0 {
		cfg.Captcha.MaxRetry = 3
	}
	return cfg, nil
}
