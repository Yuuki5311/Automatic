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
	Web     WebConfig     `yaml:"web"`
	Log     LogConfig     `yaml:"log"`
}

type JYMConfig struct {
	BaseURL    string `yaml:"base_url"`     // https://merchant.jiaoyimao.com
	LoginURL   string `yaml:"login_url"`    // 登录页完整URL（为空时用 base_url + "/login"）
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
	StatsDir string       `yaml:"stats_dir"`
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

type WebConfig struct {
	Addr string `yaml:"addr"` // 监听地址，默认 "127.0.0.1:8080"
}

type LogConfig struct {
	Level      string `yaml:"level"`       // "debug"|"info"|"warn"|"error"，默认 "info"
	File       string `yaml:"file"`        // 日志文件路径，为空则仅输出到控制台
	MaxSizeMB  int    `yaml:"max_size_mb"` // 单文件大小上限（MB），默认 10
	MaxBackups int    `yaml:"max_backups"` // 保留的轮转文件数，默认 3
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
	if cfg.Scraper.StatsDir == "" {
		cfg.Scraper.StatsDir = "./data/stats"
	}
	if cfg.Captcha.MaxRetry == 0 {
		cfg.Captcha.MaxRetry = 3
	}
	if cfg.Web.Addr == "" {
		cfg.Web.Addr = "127.0.0.1:8080"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.MaxSizeMB == 0 {
		cfg.Log.MaxSizeMB = 10
	}
	if cfg.Log.MaxBackups == 0 {
		cfg.Log.MaxBackups = 3
	}
	return cfg, nil
}
