package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// testHTML 是集成测试用的页面：包含一个展示区、按钮、输入框和输出区。
const testHTML = `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body>
<div id="app">初始内容</div>
<button id="btn">点击按钮</button>
<input id="input" />
<p id="output"></p>
<script>
document.getElementById('btn').addEventListener('click', function () {
  document.getElementById('output').textContent = '已点击';
});
</script>
</body>
</html>`

// TestHeadlessBrowserIntegration 使用真实的无头 Chrome 验证浏览器管理器与全部动作封装。
//
// 验证点：
//  1. NewManager + NewContext 启动无头浏览器并导航、等待、提取文本
//  2. Click 触发页面 JS 事件（headless 模式下不抢占鼠标）
//  3. Input 输入文本（含中文）
//  4. ExtractHTML 提取 outerHTML
//  5. Screenshot 全页截图并写出非空文件
//  6. SetCookies 通过 CDP 注入 HTTPOnly Cookie，并在后续请求中验证其生效
//  7. NewTabContext 创建独立标签页可并行操作
//
// 机器上未安装 Chrome 时自动跳过。
func TestHeadlessBrowserIntegration(t *testing.T) {
	chromePath, err := findChrome()
	if err != nil {
		t.Skipf("Chrome not found, skipping integration test: %v", err)
	}

	// 本地 HTTP 服务：/ 提供测试页面，/check 回显收到的 Cookie 头
	var mu sync.Mutex
	receivedCookie := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testHTML)
	})
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedCookie = r.Header.Get("Cookie")
		mu.Unlock()
		fmt.Fprint(w, "ok")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config.BrowserConfig{
		Headless:   true, // 无头模式：不显示窗口、不抢占鼠标
		ChromePath: chromePath,
		TimeoutSec: 60,
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := mgr.NewContext(40)
	defer cancel()

	// 1. 导航 + 等待元素 + 提取文本
	var got string
	if err := chromedp.Run(ctx,
		Navigate(srv.URL+"/"),
		WaitVisible("#app"),
		ExtractText("#app", &got),
	); err != nil {
		t.Fatalf("navigate/wait/extract failed: %v", err)
	}
	if got != "初始内容" {
		t.Errorf("ExtractText = %q, want 初始内容", got)
	}

	// 2. 点击按钮，验证 JS 事件生效
	if err := chromedp.Run(ctx,
		Click("#btn"),
		WaitVisible("#output"),
		ExtractText("#output", &got),
	); err != nil {
		t.Fatalf("click failed: %v", err)
	}
	if got != "已点击" {
		t.Errorf("after Click, #output = %q, want 已点击", got)
	}

	// 3. 输入文本（含中文）；input 元素无文本内容，通过 value 属性验证
	if err := chromedp.Run(ctx,
		Input("#input", "hello-世界"),
		chromedp.Value("#input", &got, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("input failed: %v", err)
	}
	if got != "hello-世界" {
		t.Errorf("after Input, #input value = %q, want hello-世界", got)
	}

	// 4. 提取 outerHTML
	var html string
	if err := chromedp.Run(ctx, ExtractHTML("#app", &html)); err != nil {
		t.Fatalf("extract html failed: %v", err)
	}
	if !strings.Contains(html, "初始内容") {
		t.Errorf("ExtractHTML = %q, want it to contain 初始内容", html)
	}

	// 5. 全页截图并验证文件非空
	shotPath := filepath.Join(t.TempDir(), "shot.png")
	if err := chromedp.Run(ctx, Screenshot(shotPath)); err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	info, err := os.Stat(shotPath)
	if err != nil {
		t.Fatalf("screenshot file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Error("screenshot file is empty")
	}

	// 6. 通过 CDP 注入 HTTPOnly Cookie（JS 无法设置这类 Cookie），
	//    然后导航到 /check 验证服务端实际收到了该 Cookie
	cookies := []models.CookieEntry{
		{
			Name:     "jym_token",
			Value:    "secret-token-value",
			Domain:   "127.0.0.1",
			Path:     "/",
			Expires:  float64(time.Now().Add(24 * time.Hour).Unix()),
			HTTPOnly: true,
		},
	}
	if err := chromedp.Run(ctx,
		SetCookies(cookies),
		Navigate(srv.URL+"/check"),
	); err != nil {
		t.Fatalf("set cookies failed: %v", err)
	}
	mu.Lock()
	cookieHeader := receivedCookie
	mu.Unlock()
	if !strings.Contains(cookieHeader, "jym_token=secret-token-value") {
		t.Errorf("server did not receive injected HTTPOnly cookie, got header: %q", cookieHeader)
	}

	// 7. 独立标签页：NewTabContext 可并行导航操作
	ctx2, cancel2 := mgr.NewTabContext(30)
	defer cancel2()
	if err := chromedp.Run(ctx2,
		Navigate(srv.URL+"/"),
		WaitVisible("#app"),
		ExtractText("#app", &got),
	); err != nil {
		t.Fatalf("new tab navigation failed: %v", err)
	}
	if got != "初始内容" {
		t.Errorf("tab context ExtractText = %q, want 初始内容", got)
	}
}
