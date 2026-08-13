package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

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

func TestHeadlessBrowserIntegration(t *testing.T) {
	chromePath, err := findChrome()
	if err != nil {
		t.Skipf("Chrome not found: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(testHTML))
		case "/cookie-check":
			c, err := r.Cookie("jym_token")
			if err != nil || c.Value != "secret-value" {
				http.Error(w, "missing cookie", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mgr, err := NewManager(&config.BrowserConfig{
		Headless:   true,
		ChromePath: chromePath,
		TimeoutSec: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx, cancel := mgr.NewContext(40)
	defer cancel()

	if err := Navigate(ctx, srv.URL+"/"); err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	if err := WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	text, err := ExtractText(ctx, "#app")
	if err != nil || !strings.Contains(text, "初始内容") {
		t.Fatalf("ExtractText = %q err=%v", text, err)
	}
	if err := Click(ctx, "#btn"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if err := Sleep(ctx, 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	out, err := ExtractText(ctx, "#output")
	if err != nil || out != "已点击" {
		t.Fatalf("output = %q err=%v", out, err)
	}
	if err := Input(ctx, "#input", "你好Rod"); err != nil {
		t.Fatalf("Input: %v", err)
	}
	html, err := ExtractHTML(ctx, "#app")
	if err != nil || !strings.Contains(html, "初始内容") {
		t.Fatalf("ExtractHTML = %q err=%v", html, err)
	}
	shotPath := filepath.Join(t.TempDir(), "shot.png")
	if err := Screenshot(ctx, shotPath); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if fi, err := os.Stat(shotPath); err != nil || fi.Size() == 0 {
		t.Fatalf("screenshot file invalid: %v", err)
	}

	if err := SetCookies(ctx, []models.CookieEntry{{
		Name: "jym_token", Value: "secret-value", Domain: strings.TrimPrefix(srv.URL, "http://"), Path: "/",
	}}); err != nil {
		t.Fatalf("SetCookies: %v", err)
	}
	if err := Navigate(ctx, srv.URL+"/cookie-check"); err != nil {
		t.Fatalf("Navigate cookie-check: %v", err)
	}
	body, err := ExtractText(ctx, "body")
	if err != nil || !strings.Contains(body, "ok") {
		t.Fatalf("cookie check body=%q err=%v", body, err)
	}

	ctx2, cancel2 := mgr.NewTabContext(40)
	defer cancel2()
	if err := Navigate(ctx2, srv.URL+"/"); err != nil {
		t.Fatalf("tab2 Navigate: %v", err)
	}
	t2, err := ExtractText(ctx2, "#app")
	if err != nil || !strings.Contains(t2, "初始内容") {
		t.Fatalf("tab2 text=%q err=%v", t2, err)
	}
	_ = fmt.Sprintf("ok")
}
