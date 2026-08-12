package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testYAML is a minimal-but-representative config used as a test fixture.
const testYAML = `jiaoyimao:
  base_url: "https://merchant.jiaoyimao.com"
  username: "test_user"
  password: "test_pass"
  cookie_path: "./data/cookies.json"
  login_type: "password"

feishu:
  app_id: "cli_test"
  app_secret: "secret_test"
  bitable_id: "tblTest"
  table_mapping:
    "火影忍者": "tblXXXXXXX1"
    "原神_table1": "tblXXXXXXX2"

browser:
  headless: true
  chrome_path: "/usr/bin/google-chrome"
  timeout_sec: 120
  debug_port: 9223

scraper:
  mode: "browser"
  cron_expr: "0 */2 * * *"
  games:
    - name: "火影忍者"
      url: "/workbench/recycle/naruto"
      table_count: 1
    - name: "原神"
      url: "/workbench/recycle/genshin"
      table_count: 2

captcha:
  provider: "chaojiying"
  api_key: "key_test"
  max_retry: 5
`

// writeTempConfig writes content to a temp file and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeTempConfig(t, testYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// jiaoyimao
	if cfg.JYM.BaseURL != "https://merchant.jiaoyimao.com" {
		t.Errorf("JYM.BaseURL = %q, want %q", cfg.JYM.BaseURL, "https://merchant.jiaoyimao.com")
	}
	if cfg.JYM.Username != "test_user" {
		t.Errorf("JYM.Username = %q, want %q", cfg.JYM.Username, "test_user")
	}
	if cfg.JYM.Password != "test_pass" {
		t.Errorf("JYM.Password = %q, want %q", cfg.JYM.Password, "test_pass")
	}
	if cfg.JYM.CookiePath != "./data/cookies.json" {
		t.Errorf("JYM.CookiePath = %q, want %q", cfg.JYM.CookiePath, "./data/cookies.json")
	}
	if cfg.JYM.LoginType != "password" {
		t.Errorf("JYM.LoginType = %q, want %q", cfg.JYM.LoginType, "password")
	}

	// feishu
	if cfg.Feishu.AppID != "cli_test" {
		t.Errorf("Feishu.AppID = %q, want %q", cfg.Feishu.AppID, "cli_test")
	}
	if cfg.Feishu.AppSecret != "secret_test" {
		t.Errorf("Feishu.AppSecret = %q, want %q", cfg.Feishu.AppSecret, "secret_test")
	}
	if cfg.Feishu.BitableID != "tblTest" {
		t.Errorf("Feishu.BitableID = %q, want %q", cfg.Feishu.BitableID, "tblTest")
	}
	if got := cfg.Feishu.TableMapping["火影忍者"]; got != "tblXXXXXXX1" {
		t.Errorf(`Feishu.TableMapping["火影忍者"] = %q, want "tblXXXXXXX1"`, got)
	}
	if got := cfg.Feishu.TableMapping["原神_table1"]; got != "tblXXXXXXX2" {
		t.Errorf(`Feishu.TableMapping["原神_table1"] = %q, want "tblXXXXXXX2"`, got)
	}
	if len(cfg.Feishu.TableMapping) != 2 {
		t.Errorf("Feishu.TableMapping length = %d, want 2", len(cfg.Feishu.TableMapping))
	}

	// browser
	if !cfg.Browser.Headless {
		t.Error("Browser.Headless = false, want true")
	}
	if cfg.Browser.ChromePath != "/usr/bin/google-chrome" {
		t.Errorf("Browser.ChromePath = %q, want %q", cfg.Browser.ChromePath, "/usr/bin/google-chrome")
	}
	if cfg.Browser.TimeoutSec != 120 {
		t.Errorf("Browser.TimeoutSec = %d, want 120", cfg.Browser.TimeoutSec)
	}
	if cfg.Browser.DebugPort != 9223 {
		t.Errorf("Browser.DebugPort = %d, want 9223", cfg.Browser.DebugPort)
	}

	// scraper
	if cfg.Scraper.Mode != "browser" {
		t.Errorf("Scraper.Mode = %q, want %q", cfg.Scraper.Mode, "browser")
	}
	if cfg.Scraper.CronExpr != "0 */2 * * *" {
		t.Errorf("Scraper.CronExpr = %q, want %q", cfg.Scraper.CronExpr, "0 */2 * * *")
	}
	if len(cfg.Scraper.Games) != 2 {
		t.Fatalf("Scraper.Games length = %d, want 2", len(cfg.Scraper.Games))
	}
	g0 := cfg.Scraper.Games[0]
	if g0.Name != "火影忍者" || g0.URL != "/workbench/recycle/naruto" || g0.TableCount != 1 {
		t.Errorf("Games[0] = %+v, want 火影忍者/naruto/1", g0)
	}
	g1 := cfg.Scraper.Games[1]
	if g1.Name != "原神" || g1.URL != "/workbench/recycle/genshin" || g1.TableCount != 2 {
		t.Errorf("Games[1] = %+v, want 原神/genshin/2", g1)
	}

	// captcha
	if cfg.Captcha.Provider != "chaojiying" {
		t.Errorf("Captcha.Provider = %q, want %q", cfg.Captcha.Provider, "chaojiying")
	}
	if cfg.Captcha.APIKey != "key_test" {
		t.Errorf("Captcha.APIKey = %q, want %q", cfg.Captcha.APIKey, "key_test")
	}
	if cfg.Captcha.MaxRetry != 5 {
		t.Errorf("Captcha.MaxRetry = %d, want 5", cfg.Captcha.MaxRetry)
	}
}

func TestLoadDefaults(t *testing.T) {
	// Config with only a browser section; everything else left empty.
	path := writeTempConfig(t, "browser:\n  headless: false\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Browser.TimeoutSec != 60 {
		t.Errorf("Browser.TimeoutSec default = %d, want 60", cfg.Browser.TimeoutSec)
	}
	if cfg.Scraper.Mode != "auto" {
		t.Errorf("Scraper.Mode default = %q, want %q", cfg.Scraper.Mode, "auto")
	}
	if cfg.Captcha.MaxRetry != 3 {
		t.Errorf("Captcha.MaxRetry default = %d, want 3", cfg.Captcha.MaxRetry)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("Load() with missing file: expected error, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTempConfig(t, "jiaoyimao: [unclosed\n  not: valid")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() with invalid YAML: expected error, got nil")
	}
}

// TestLoadTestConfigFile 集成校验：仓库内 configs/config.test.yaml 必须能正常加载，
// 且只含测试占位值（tblTEST* 表格ID、无真实凭据），确保试运行不会影响生产数据。
func TestLoadTestConfigFile(t *testing.T) {
	// 测试工作目录是包目录，向上两级找到仓库根目录
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	path := filepath.Join(repoRoot, "configs", "config.test.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("configs/config.test.yaml 不存在，跳过: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", path, err)
	}

	// 关键字段应被正确解析
	if cfg.JYM.CookiePath != "./data/cookies.test.json" {
		t.Errorf("JYM.CookiePath = %q, want ./data/cookies.test.json（独立于生产Cookie）", cfg.JYM.CookiePath)
	}
	if cfg.Scraper.Mode != "auto" {
		t.Errorf("Scraper.Mode = %q, want auto", cfg.Scraper.Mode)
	}
	if len(cfg.Scraper.Games) != 6 {
		t.Errorf("Scraper.Games length = %d, want 6", len(cfg.Scraper.Games))
	}

	// 安全校验：表格ID必须全部是 tblTEST* 前缀，杜绝误连生产表格
	for game, tableID := range cfg.Feishu.TableMapping {
		if !strings.HasPrefix(tableID, "tblTEST") {
			t.Errorf("游戏 %s 的表格ID %q 不是测试表格（缺少 tblTEST 前缀）", game, tableID)
		}
	}
	if !strings.HasPrefix(cfg.Feishu.BitableID, "bascnTEST") {
		t.Errorf("BitableID = %q, want bascnTEST 前缀", cfg.Feishu.BitableID)
	}

	// 安全校验：不得包含真实凭据占位格式（cli_/tblXXX 等生产模板标记）
	for _, secret := range []string{cfg.Feishu.AppSecret, cfg.JYM.Password} {
		if secret == "" || secret == "YOUR_PASSWORD" || secret == "xxx" {
			t.Errorf("配置中疑似包含生产占位凭据: %q", secret)
		}
	}
}
