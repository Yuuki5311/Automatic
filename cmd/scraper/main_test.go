// 主程序入口的单元测试与冒烟测试。
//
// 冒烟测试（TestBinary*）：编译出真实可执行文件 bin 副本，验证
// 命令行入口在配置缺失等失败场景下能正确报错退出（不依赖真实
// 浏览器、账号或飞书凭据）。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/captcha"
	"github.com/example/jiaoyimao-scraper/internal/config"
)

// TestMain 为冒烟测试创建包级临时目录（测试运行期间持久存在，避免
// 每个测试的 t.TempDir 被提前清理导致已编译的可执行文件丢失）。
func TestMain(m *testing.M) {
	var err error
	binDir, err = os.MkdirTemp("", "scraper-smoke-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时目录失败: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(binDir)
	os.Exit(code)
}

func TestNewCaptchaSolver(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.CaptchaConfig
		wantType string
	}{
		{name: "opencv本地识别", cfg: &config.CaptchaConfig{Provider: "opencv", MaxRetry: 5}, wantType: "*captcha.SliderSolver"},
		{name: "超级鹰第三方", cfg: &config.CaptchaConfig{Provider: "chaojiying", APIKey: "k"}, wantType: "*captcha.ThirdPartySolver"},
		{name: "2captcha第三方", cfg: &config.CaptchaConfig{Provider: "2captcha", APIKey: "k"}, wantType: "*captcha.ThirdPartySolver"},
		{name: "provider为空默认opencv", cfg: &config.CaptchaConfig{}, wantType: "*captcha.SliderSolver"},
		{name: "配置为nil默认opencv", cfg: nil, wantType: "*captcha.SliderSolver"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solver := newCaptchaSolver(tt.cfg)
			if solver == nil {
				t.Fatal("newCaptchaSolver 返回 nil")
			}
			if tt.wantType == "*captcha.SliderSolver" {
				if _, ok := solver.(*captcha.SliderSolver); !ok {
					t.Fatalf("期望 %s，实际类型 %T", tt.wantType, solver)
				}
			} else {
				if _, ok := solver.(*captcha.ThirdPartySolver); !ok {
					t.Fatalf("期望 %s，实际类型 %T", tt.wantType, solver)
				}
			}
		})
	}
}

// buildOnce 只编译一次可执行文件，供多个冒烟测试复用。
var (
	buildOnce sync.Once
	binDir    string
	binPath   string
	buildErr  error
)

// buildBinary 编译当前 main 包到临时目录，返回可执行文件路径。
func buildBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		exeName := "scraper"
		if runtime.GOOS == "windows" {
			exeName += ".exe"
		}
		binPath = filepath.Join(binDir, exeName)

		// 测试的工作目录是包目录；为稳妥起见仍显式指定。
		_, thisFile, _, _ := runtime.Caller(0)
		pkgDir := filepath.Dir(thisFile)

		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = pkgDir
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

// runBinary 运行可执行文件并返回退出码与合并输出。
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

// TestBinaryMissingConfig 冒烟测试：配置文件不存在时应以非零码退出并报错。
func TestBinaryMissingConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nonexistent.yaml")
	code, output := runBinary(t, "-config", missing)
	if code == 0 {
		t.Fatalf("期望非零退出码，实际为 0，输出: %s", output)
	}
	if !strings.Contains(output, "加载配置失败") {
		t.Fatalf("输出中未包含配置加载失败信息，输出: %s", output)
	}
}

// TestBinaryBadCronExpr 冒烟测试：守护进程模式下非法 cron 表达式应直接报错退出
// （验证 cron 调度接入正常，且不会静默启动）。
func TestBinaryBadCronExpr(t *testing.T) {
	cfgPath := filepath.Join(binDir, "bad-cron.yaml")
	content := "scraper:\n  cron_expr: \"not-a-cron-expr\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}

	code, output := runBinary(t, "-daemon", "-config", cfgPath)
	if code == 0 {
		t.Fatalf("期望非零退出码，实际为 0，输出: %s", output)
	}
	if !strings.Contains(output, "无效的定时表达式") {
		t.Fatalf("输出中未包含 cron 表达式错误信息，输出: %s", output)
	}
}

// TestBinaryUnknownFlag 冒烟测试：未知参数应输出用法说明并以非零码退出
// （flag 包对 -h 采用成功退出码 0，故用未定义参数验证错误路径）。
func TestBinaryUnknownFlag(t *testing.T) {
	code, output := runBinary(t, "-bogus")
	if code == 0 {
		t.Fatalf("期望非零退出码，实际为 0，输出: %s", output)
	}
	if !strings.Contains(output, "flag provided but not defined") {
		t.Fatalf("输出中未包含 flag 错误信息，输出: %s", output)
	}
	for _, want := range []string{"-config", "-once", "-daemon", "-web"} {
		if !strings.Contains(output, want) {
			t.Fatalf("用法说明中缺少 %s，输出: %s", want, output)
		}
	}
}
