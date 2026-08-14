package browser

import "path/filepath"

// Config 浏览器管理器启动配置（对应主项目 internal/browser.Config 与 config.BrowserConfig）。
type Config struct {
	Headless              bool   // 无头模式
	BrowserPath           string // 自定义 Chrome/Edge 可执行文件路径，空则自动 LookPath
	ProfileRoot           string // 用户数据根目录，默认 runtime/data/browser
	ClearProfileDir       bool   // 每次启动前清空 profile 目录
	UseStoreProfile       bool   // 按店铺复用固定 profile（默认 true）
	StoreProfileSlots     int    // 每店铺并行 profile 槽位数，0 表示 1
	FullScreen            bool
	Kiosk                 bool
	WindowSize            string // "width,height"，默认 "1166,1012"
	WindowPosition        string // "x,y"
	Proxy                 string // 代理，可写 host:port 或完整 URL
	MaxConcurrentSessions int    // 全局并发浏览器会话上限，默认 3，最大 10
}

// DefaultConfig 返回与主项目 config.go 默认值一致的配置。
func DefaultConfig() Config {
	return Config{
		Headless:              false,
		ProfileRoot:           filepath.Join("runtime", "data", "browser"),
		ClearProfileDir:       false,
		UseStoreProfile:       true,
		WindowSize:            "1166,1012",
		MaxConcurrentSessions: 3,
	}
}
