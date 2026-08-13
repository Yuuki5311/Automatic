package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestPerformLoginAndSave_WritesCookies(t *testing.T) {
	want := &models.CookieData{Cookies: []models.CookieEntry{{Name: "token", Value: "abc"}}}
	var savedPath string
	var saved *models.CookieData

	err := performLoginAndSave(context.Background(), &config.Config{
		JYM: config.JYMConfig{CookiePath: "data/cookies.json"},
	}, nil, func(ctx context.Context, cfg *config.Config, solver captcha.Solver) (*models.CookieData, error) {
		return want, nil
	}, func(path string, data *models.CookieData) error {
		savedPath = path
		saved = data
		return nil
	})
	if err != nil {
		t.Fatalf("performLoginAndSave: %v", err)
	}
	if savedPath != "data/cookies.json" {
		t.Fatalf("save path = %q", savedPath)
	}
	if saved != want {
		t.Fatalf("saved cookies = %#v, want %#v", saved, want)
	}
}

func TestPerformLoginAndSave_LoginErrorSkipsSave(t *testing.T) {
	saved := false
	err := performLoginAndSave(context.Background(), &config.Config{
		JYM: config.JYMConfig{CookiePath: "data/cookies.json"},
	}, nil, func(ctx context.Context, cfg *config.Config, solver captcha.Solver) (*models.CookieData, error) {
		return nil, errors.New("login failed")
	}, func(path string, data *models.CookieData) error {
		saved = true
		return nil
	})
	if err == nil {
		t.Fatal("expected login error")
	}
	if saved {
		t.Fatal("SaveCookies should not run when login fails")
	}
}

func TestNewCaptchaSolver(t *testing.T) {
	solver := newCaptchaSolver(&config.CaptchaConfig{Provider: "opencv"})
	if _, ok := solver.(*captcha.SliderSolver); !ok {
		t.Fatalf("opencv provider: got %T", solver)
	}
	solver = newCaptchaSolver(&config.CaptchaConfig{Provider: "chaojiying"})
	if _, ok := solver.(*captcha.ThirdPartySolver); !ok {
		t.Fatalf("chaojiying provider: got %T", solver)
	}
}

var (
	buildOnce sync.Once
	binDir    string
	binPath   string
	buildErr  error
)

func TestMain(m *testing.M) {
	var err error
	binDir, err = os.MkdirTemp("", "cookie-tool-smoke-*")
	if err != nil {
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(binDir)
	os.Exit(code)
}

func buildBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		exeName := "cookie-tool"
		if runtime.GOOS == "windows" {
			exeName += ".exe"
		}
		binPath = filepath.Join(binDir, exeName)
		_, thisFile, _, _ := runtime.Caller(0)
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = filepath.Dir(thisFile)
		if output, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Fatalf("go build 失败: %v\n%s", err, output)
		}
	})
	if buildErr != nil {
		t.Fatalf("可执行文件编译失败: %v", buildErr)
	}
	return binPath
}

func runBinary(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(buildBinary(t), args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("运行可执行文件失败: %v", err)
	}
	return exitErr.ExitCode(), string(output)
}

func TestBinaryUnknownFlagListsLogin(t *testing.T) {
	code, output := runBinary(t, "-bogus")
	if code == 0 {
		t.Fatalf("期望非零退出码，实际为 0，输出: %s", output)
	}
	if !strings.Contains(output, "-login") {
		t.Fatalf("用法说明中缺少 -login，输出: %s", output)
	}
}

func TestBinaryMissingConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nonexistent.yaml")
	code, output := runBinary(t, "-login", "-config", missing)
	if code == 0 {
		t.Fatalf("期望非零退出码，实际为 0，输出: %s", output)
	}
	if !strings.Contains(output, "加载配置失败") {
		t.Fatalf("输出中未包含配置加载失败信息，输出: %s", output)
	}
}
