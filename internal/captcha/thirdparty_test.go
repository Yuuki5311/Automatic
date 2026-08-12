package captcha

import (
	"context"
	"strings"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

func TestNewThirdPartySolverDefaults(t *testing.T) {
	s := NewThirdPartySolver(nil)
	if s.Type() != "thirdparty:chaojiying" {
		t.Errorf("Type() = %q, want \"thirdparty:chaojiying\"", s.Type())
	}
	if s.apiKey != "" {
		t.Errorf("apiKey 应为空, got %q", s.apiKey)
	}
}

func TestNewThirdPartySolverWithConfig(t *testing.T) {
	s := NewThirdPartySolver(&config.CaptchaConfig{Provider: "2captcha", APIKey: "secret-key"})
	if s.Type() != "thirdparty:2captcha" {
		t.Errorf("Type() = %q, want \"thirdparty:2captcha\"", s.Type())
	}
	if s.apiKey != "secret-key" {
		t.Errorf("apiKey = %q, want %q", s.apiKey, "secret-key")
	}
}

func TestThirdPartySolverSolveUnsupportedProvider(t *testing.T) {
	s := NewThirdPartySolver(&config.CaptchaConfig{Provider: "unknown"})
	err := s.Solve(context.Background())
	if err == nil {
		t.Fatal("不支持的 provider 应返回错误")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("错误信息应包含 provider 名: %v", err)
	}
}

func TestThirdPartySolverSolve2Captcha(t *testing.T) {
	s := NewThirdPartySolver(&config.CaptchaConfig{Provider: "2captcha", APIKey: "k"})
	if err := s.Solve(context.Background()); err == nil {
		t.Error("2captcha 未实现时应返回错误")
	}
}

func TestThirdPartySolverSolveChaojiyingWithoutKey(t *testing.T) {
	s := NewThirdPartySolver(&config.CaptchaConfig{Provider: "chaojiying"})
	err := s.Solve(context.Background())
	if err == nil {
		t.Fatal("未配置 api_key 时应返回错误")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("错误信息应提示缺少 api_key: %v", err)
	}
}
