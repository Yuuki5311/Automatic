package auth

// 集成测试：使用真实无头 Chrome 对本地模拟的"登录页 → 工作台"执行完整
// 自动登录流程，验证 PerformLogin / RefreshIfNeeded 与 CDP Cookie 提取。
//
// 默认跳过。需要本机安装 Chrome（或用 CHROME_PATH 指定），并设置环境变量：
//
//	JYM_E2E=1 go test ./internal/auth/ -run TestLoginIntegration -v -timeout 120s
//
// 模拟站点行为（与交易猫真实页面结构对齐）：
//   - GET /login     渲染登录表单（账号/密码输入框 + 提交按钮）
//   - POST /login    校验通过后写入 HTTPOnly Cookie 并 302 跳转 /workbench
//   - GET /workbench 渲染带 .workbench 类的工作台页面

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/browser"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

const loginPageHTML = `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body>
<form method="POST" action="/login">
  <input type="text" name="username" placeholder="请输入手机号">
  <input type="password" name="password" placeholder="请输入密码">
  <button type="submit" class="login-btn">登录</button>
</form>
</body>
</html>`

// findTestChrome 返回本机 Chrome 路径，找不到时返回空字符串。
func findTestChrome() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	paths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// TestLoginIntegration 端到端验证自动登录与Cookie刷新。
func TestLoginIntegration(t *testing.T) {
	if os.Getenv("JYM_E2E") != "1" {
		t.Skip("JYM_E2E=1 not set, skipping headless Chrome integration test")
	}
	chromePath := findTestChrome()
	if chromePath == "" {
		t.Skip("Chrome not found, skipping integration test")
	}

	// 1. 启动模拟站点
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// 模拟登录成功：写入 HTTPOnly Cookie 并跳转工作台
			http.SetCookie(w, &http.Cookie{
				Name:     "jym_token",
				Value:    "integration-secret",
				Path:     "/",
				HttpOnly: true,
			})
			http.Redirect(w, r, "/workbench", http.StatusFound)
			return
		}
		fmt.Fprint(w, loginPageHTML)
	})
	mux.HandleFunc("/workbench", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<div class="workbench">工作台</div>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 2. 启动无头浏览器（headless：不显示窗口、不抢占鼠标）
	mgr, err := browser.NewManager(&config.BrowserConfig{
		Headless:   true,
		ChromePath: chromePath,
	})
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := mgr.NewContext(60)
	defer cancel()

	cfg := &config.Config{JYM: config.JYMConfig{
		BaseURL:    srv.URL,
		Username:   "testuser",
		Password:   "testpass",
		LoginType:  "password",
		CookiePath: filepath.Join(t.TempDir(), "cookies.json"),
	}}

	// 3. 第一次 RefreshIfNeeded：Cookie 文件不存在 → 触发完整自动登录
	//    并持久化 Cookie；stubSolver 模拟"立即检测到验证码并解决"。
	first, err := (&LoginService{}).RefreshIfNeeded(ctx, cfg, &stubSolver{detectResult: true})
	if err != nil {
		t.Fatalf("RefreshIfNeeded (first login) failed: %v", err)
	}
	assertSessionCookie(t, first.Cookies)

	// 4. 验证 Cookie 已持久化到磁盘，且包含 HTTPOnly 的 jym_token
	saved, err := LoadCookies(cfg.JYM.CookiePath)
	if err != nil {
		t.Fatalf("LoadCookies after login failed: %v", err)
	}
	assertSessionCookie(t, saved.Cookies)

	// 5. 第二次 RefreshIfNeeded：Cookie 有效 → 跳过登录直接复用
	second, err := (&LoginService{}).RefreshIfNeeded(ctx, cfg, &stubSolver{})
	if err != nil {
		t.Fatalf("RefreshIfNeeded (reuse) failed: %v", err)
	}
	if len(second.Cookies) == 0 {
		t.Fatal("RefreshIfNeeded reuse returned empty cookies")
	}
	if second.Cookies[0].Value != first.Cookies[0].Value {
		t.Fatalf("reused cookie value = %q, want %q", second.Cookies[0].Value, first.Cookies[0].Value)
	}
}

// assertSessionCookie 断言 Cookie 列表中包含 HTTPOnly 的 jym_token。
func assertSessionCookie(t *testing.T, cookies []models.CookieEntry) {
	t.Helper()
	for _, c := range cookies {
		if c.Name == "jym_token" {
			if c.Value != "integration-secret" {
				t.Fatalf("jym_token value = %q, want integration-secret", c.Value)
			}
			if !c.HTTPOnly {
				t.Fatal("jym_token should be HTTPOnly (CDP must extract it)")
			}
			return
		}
	}
	t.Fatalf("jym_token cookie not extracted, got: %s", formatCookies(cookies))
}

// formatCookies 将Cookie列表格式化为JSON（仅用于失败信息）。
func formatCookies(cookies []models.CookieEntry) string {
	b, err := json.Marshal(cookies)
	if err != nil {
		return fmt.Sprintf("%+v", cookies)
	}
	return string(b)
}
