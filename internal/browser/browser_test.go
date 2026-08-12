package browser

import (
	"os"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

// testManagerConfig 构造一个不会真正启动浏览器的配置（ChromePath 指向不存在的路径，
// NewManager 只记录配置、不会立即启动浏览器进程，因此可在单测中使用）。
func testManagerConfig() *config.BrowserConfig {
	return &config.BrowserConfig{
		Headless:   true,
		ChromePath: `C:\fake\path\chrome.exe`,
		TimeoutSec: 30,
		DebugPort:  0,
	}
}

func TestNewManager(t *testing.T) {
	m, err := NewManager(testManagerConfig())
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	if m == nil {
		t.Fatal("NewManager returned nil manager")
	}
	if m.allocCtx == nil {
		t.Error("allocCtx is nil")
	}
	if m.allocCancel == nil {
		t.Error("allocCancel is nil")
	}
	if len(m.opts) == 0 {
		t.Error("expected non-empty exec allocator options")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestNewManagerWithDebugPort(t *testing.T) {
	cfg := testManagerConfig()
	cfg.DebugPort = 9222
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager with DebugPort returned error: %v", err)
	}
	defer m.Close()
	if len(m.opts) == 0 {
		t.Error("expected non-empty exec allocator options")
	}
}

func TestNewManagerAutoFindChrome(t *testing.T) {
	// ChromePath 为空时应自动查找 Chrome；即使找不到也不应返回错误（浏览器未启动）。
	m, err := NewManager(&config.BrowserConfig{Headless: true})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	defer m.Close()
	if len(m.opts) == 0 {
		t.Error("expected non-empty exec allocator options")
	}
}

func TestNewContextTimeout(t *testing.T) {
	m, err := NewManager(testManagerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// timeoutSec > 0 时应带截止时间
	ctx, cancel := m.NewContext(30)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline when timeoutSec=30")
	}
	if d := time.Until(deadline); d > 31*time.Second || d < 29*time.Second {
		t.Errorf("unexpected deadline in %v, want ~30s", d)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("fresh context should not be canceled, got %v", err)
	}

	// timeoutSec = 0 时不应带截止时间
	ctx2, cancel2 := m.NewContext(0)
	defer cancel2()
	if _, ok := ctx2.Deadline(); ok {
		t.Error("expected no deadline when timeoutSec=0")
	}
}

func TestNewTabContextIsIndependentContext(t *testing.T) {
	m, err := NewManager(testManagerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	ctx1, cancel1 := m.NewContext(30)
	defer cancel1()
	ctx2, cancel2 := m.NewTabContext(30)
	defer cancel2()

	if ctx1 == ctx2 {
		t.Error("NewContext and NewTabContext should return distinct contexts")
	}
	if _, ok := ctx2.Deadline(); !ok {
		t.Error("expected a deadline on NewTabContext when timeoutSec=30")
	}

	// 取消其中一个不应影响另一个
	cancel1()
	if ctx2.Err() != nil {
		t.Error("cancelling the first context must not cancel the tab context")
	}
}

func TestFindChrome(t *testing.T) {
	path, err := findChrome()
	if err != nil {
		// 机器上没有安装 Chrome 不算失败，只是跳过校验
		t.Skipf("Chrome not found, skipping: %v", err)
	}
	if path == "" {
		t.Fatal("findChrome returned an empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("findChrome returned non-existent path %q: %v", path, err)
	}
}
