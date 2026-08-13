package browser

import (
	"os"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

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
	if m.cfg == nil {
		t.Error("cfg is nil")
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
}

func TestNewManagerAutoFindChrome(t *testing.T) {
	m, err := NewManager(&config.BrowserConfig{Headless: true})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	defer m.Close()
}

func TestNewContextTimeout(t *testing.T) {
	m, err := NewManager(testManagerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

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

	cancel1()
	if ctx2.Err() != nil {
		t.Error("cancelling the first context must not cancel the tab context")
	}
}

func TestPageFromContextInvalid(t *testing.T) {
	_, err := PageFromContext(t.Context())
	if err == nil {
		t.Fatal("expected error for plain context")
	}
}

func TestFindChrome(t *testing.T) {
	path, err := findChrome()
	if err != nil {
		t.Skipf("Chrome not found, skipping: %v", err)
	}
	if path == "" {
		t.Fatal("findChrome returned an empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("findChrome returned non-existent path %q: %v", path, err)
	}
}
