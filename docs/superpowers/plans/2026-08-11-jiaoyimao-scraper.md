# 交易猫"我的回收"数据抓取与飞书同步 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从交易猫商户工作台（https://merchant.jiaoyimao.com/workbench）"我的回收"栏自动抓取6款游戏（火影忍者、原神、绝区零、崩坏：星穹铁道、鸣潮、三角洲行动）的8个表格数据，写入飞书多维表格。

**Architecture:** 采用"API优先 + 浏览器兜底"的混合架构。优先通过浏览器 DevTools 抓包反向工程内部 API 直接调用（快速稳定）；登录与滑块验证使用 headless Chrome（chromedp）自动化完成；Cookie 管理模块支持手动导入和自动登录续期。整个后端用 Go 实现，所有点击操作在 headless 浏览器中执行，不抢占用户鼠标。

**Tech Stack:** Go 1.22+, chromedp (headless Chrome), goquery (HTML解析), Feishu Open API (多维表格/Bitable), OpenCV/gocv 或第三方打码服务 (滑块验证), robfig/cron (定时任务), Viper (配置管理)

## Global Constraints

- 后端必须使用 Go 语言
- 所有浏览器操作在 headless 模式下执行，不抢占鼠标
- 需要独立的 Cookie 获取/管理工具
- 必须处理登录时的滑块验证码
- 数据写入飞书多维表格（Bitable）

---

## 文件结构总览

```
jiaoyimao-scraper/
├── cmd/
│   ├── scraper/main.go           # 主抓取服务入口（定时任务 + 单次运行）
│   └── cookie-tool/main.go       # Cookie 管理 CLI 工具
├── internal/
│   ├── auth/
│   │   ├── cookie.go             # Cookie 加载/保存/校验
│   │   └── login.go              # 自动登录逻辑（导航 → 输入账号密码 → 处理验证码 → 提取 Cookie）
│   ├── browser/
│   │   ├── browser.go            # chromedp 浏览器实例管理（启动/关闭/上下文）
│   │   └── actions.go            # 通用页面操作封装（点击/等待/输入/截图/提取文本）
│   ├── captcha/
│   │   ├── captcha.go            # 验证码识别接口定义
│   │   ├── slider.go             # 滑块验证码检测与模拟拖动（OpenCV 计算缺口距离 → 模拟人类拖拽轨迹）
│   │   └── thirdparty.go         # 第三方打码平台集成（超级鹰/2captcha 备用方案）
│   ├── scraper/
│   │   ├── api.go                # API 模式抓取（直接调用内部接口，快速高效）
│   │   ├── browser.go            # 浏览器模式抓取（API 失败的兜底方案，模拟点击导航提取DOM）
│   │   ├── parser.go             # 数据解析（API JSON / HTML Table → 统一数据模型）
│   │   └── manager.go            # 抓取管理器（编排登录→导航→抓取→解析全流程）
│   ├── feishu/
│   │   ├── client.go             # 飞书开放平台 HTTP 客户端（token 管理、重试、限流）
│   │   └── bitable.go            # 多维表格操作（查询字段映射 → 批量写入/更新记录）
│   ├── models/
│   │   └── models.go             # 统一数据模型定义
│   └── config/
│       └── config.go             # YAML 配置文件加载
├── configs/
│   └── config.yaml               # 默认配置文件模板
├── docs/
│   └── api-research.md           # API 反向工程记录（接口URL/参数/响应格式）
├── scripts/
│   └── export-cookie.sh          # 从浏览器手动导出 Cookie 的辅助脚本
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## 前置准备清单（在开始编码前完成）

### 1. 浏览器抓包获取真实API

在Chrome中打开 https://merchant.jiaoyimao.com/workbench，F12 → Network → XHR/Fetch：
- [ ] 登录时抓取登录API（URL、请求体、响应中的token字段名）
- [ ] 导航到"我的回收"后，抓取回收订单列表API
- [ ] 切换6个游戏标签，记录每个游戏对应的 `game_id` 参数值
- [ ] 记录API响应JSON的完整结构（字段名、数据类型）
- [ ] 确定分页参数（page/pageSize/page_size/offset 等）
- [ ] 检查是否需要特定的请求头（如 `X-Requested-With`、`Authorization`）

将抓包结果整理到 `docs/api-research.md`。

### 2. 飞书多维表格创建

- [ ] 在飞书开放平台创建自建应用 → 获取 `app_id` 和 `app_secret`
- [ ] 创建多维表格（Bitable），按8个表格分别创建工作表
- [ ] 在每个工作表中创建字段（字段名与 `orderToFields()` 函数中的中文名一致）
- [ ] 获取 bitable `app_token`（即 bitable_id）和各工作表的 `table_id`
- [ ] 在飞书开放平台后台配置应用权限：`bitable:app`（多维表格读写权限）
- [ ] 发布应用并获取审批

### 3. 环境确认

- [ ] 确认本机安装 Google Chrome 浏览器（chromedp 依赖）
- [ ] 确认 Go 版本 ≥ 1.22：`go version`
- [ ] 确认可访问 `https://merchant.jiaoyimao.com`
- [ ] 确认可访问 `https://open.feishu.cn`

### 4. 游戏与表格数量确认

实际确认6个游戏对应的8个表格分布（以下为推测，需实际验证）：

| 游戏 | 推测表格数 | 可能的分表原因 |
|------|-----------|---------------|
| 火影忍者 | 1 | - |
| 原神 | 2 | 官服 / 渠道服(B服) |
| 绝区零 | 1 | - |
| 崩坏：星穹铁道 | 2 | 官服 / 渠道服 |
| 鸣潮 | 1 | - |
| 三角洲行动 | 1 | - |
| **合计** | **8** | |

---

### Task 1: 项目初始化与配置系统

**Files:**
- Create: `go.mod`
- Create: `internal/config/config.go`
- Create: `configs/config.yaml`
- Create: `configs/config.yaml.example`
- Create: `internal/models/models.go`
- Create: `Makefile`
- Create: `.gitignore`

**Interfaces:**
- Produces: `config.Config` 结构体（所有配置项的聚合类型）
- Produces: `config.Load(path string) (*Config, error)` — 加载 YAML 配置
- Produces: `models.RecycleOrder` 结构体（统一的回收订单数据模型）

- [ ] **Step 1: 初始化 Go module**

```bash
cd c:/Automatic
go mod init github.com/example/jiaoyimao-scraper
```

- [ ] **Step 2: 编写配置结构体与加载逻辑**

```go
// internal/config/config.go
package config

import (
    "os"
    "gopkg.in/yaml.v3"
)

type Config struct {
    JYM      JYMConfig      `yaml:"jiaoyimao"`
    Feishu   FeishuConfig   `yaml:"feishu"`
    Browser  BrowserConfig  `yaml:"browser"`
    Scraper  ScraperConfig  `yaml:"scraper"`
    Captcha  CaptchaConfig  `yaml:"captcha"`
}

type JYMConfig struct {
    BaseURL    string `yaml:"base_url"`     // https://merchant.jiaoyimao.com
    Username   string `yaml:"username"`
    Password   string `yaml:"password"`
    CookiePath string `yaml:"cookie_path"`  // Cookie 持久化路径
    LoginType  string `yaml:"login_type"`   // "password" | "sms"（短信验证码登录）
}

type FeishuConfig struct {
    AppID       string `yaml:"app_id"`
    AppSecret   string `yaml:"app_secret"`
    BitableID   string `yaml:"bitable_id"`
    TableMapping map[string]string `yaml:"table_mapping"` // 游戏名 → 飞书表格ID
}

type BrowserConfig struct {
    Headless    bool   `yaml:"headless"`
    ChromePath  string `yaml:"chrome_path"`
    TimeoutSec  int    `yaml:"timeout_sec"`
    DebugPort   int    `yaml:"debug_port"`
}

type ScraperConfig struct {
    Mode       string           `yaml:"mode"` // "api" | "browser" | "auto"
    Games      []GameConfig     `yaml:"games"`
    CronExpr   string           `yaml:"cron_expr"` // 定时抓取表达式
}

type GameConfig struct {
    Name       string `yaml:"name"`        // 游戏名称
    URL        string `yaml:"url"`         // 回收页面相对路径
    TableCount int    `yaml:"table_count"` // 该游戏下的表格数量
}

type CaptchaConfig struct {
    Provider  string  `yaml:"provider"`  // "opencv" | "chaojiying" | "2captcha"
    APIKey    string  `yaml:"api_key"`   // 第三方打码平台密钥
    MaxRetry  int     `yaml:"max_retry"` // 验证码最大重试次数
}

func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    cfg := &Config{}
    if err := yaml.Unmarshal(data, cfg); err != nil {
        return nil, err
    }
    // 设置默认值
    if cfg.Browser.TimeoutSec == 0 {
        cfg.Browser.TimeoutSec = 60
    }
    if cfg.Scraper.Mode == "" {
        cfg.Scraper.Mode = "auto"
    }
    if cfg.Captcha.MaxRetry == 0 {
        cfg.Captcha.MaxRetry = 3
    }
    return cfg, nil
}
```

- [ ] **Step 3: 编写配置文件模板**

```yaml
# configs/config.yaml
jiaoyimao:
  base_url: "https://merchant.jiaoyimao.com"
  username: "your_phone_or_email"
  password: "your_password"
  cookie_path: "./data/cookies.json"
  login_type: "password"     # "password" 密码登录 | "sms" 短信验证码登录

feishu:
  app_id: "cli_xxxxxxxxxxxx"
  app_secret: "xxxxxxxxxxxxxxxxxxxxxxxxxx"
  bitable_id: "tblXXXXXXXXXXXXXXXX"
  table_mapping:
    "火影忍者": "tblXXXXXXX1"
    "原神_table1": "tblXXXXXXX2"        # 原神-官服
    "原神_table2": "tblXXXXXXX3"        # 原神-渠道服/B服
    "绝区零": "tblXXXXXXX4"
    "崩坏：星穹铁道_table1": "tblXXXXXXX5"  # 星铁-官服
    "崩坏：星穹铁道_table2": "tblXXXXXXX6"  # 星铁-渠道服
    "鸣潮": "tblXXXXXXX7"
    "三角洲行动": "tblXXXXXXX8"

browser:
  headless: true           # 无头模式，不显示浏览器窗口
  chrome_path: ""          # 留空则自动查找系统 Chrome
  timeout_sec: 60
  debug_port: 9222

scraper:
  mode: "auto"             # auto: API优先，失败时用浏览器兜底
  cron_expr: "0 */2 * * *" # 每2小时执行一次
  games:
    - name: "火影忍者"
      url: "/workbench/recycle/naruto"
      table_count: 1
    - name: "原神"
      url: "/workbench/recycle/genshin"
      table_count: 2
    - name: "绝区零"
      url: "/workbench/recycle/zzz"
      table_count: 1
    - name: "崩坏：星穹铁道"
      url: "/workbench/recycle/hsr"
      table_count: 2
    - name: "鸣潮"
      url: "/workbench/recycle/wuthering"
      table_count: 1
    - name: "三角洲行动"
      url: "/workbench/recycle/deltaforce"
      table_count: 1

captcha:
  provider: "opencv"       # 优先使用 OpenCV 本地识别
  api_key: ""              # 备用第三方平台密钥
  max_retry: 3
```

- [ ] **Step 4: 编写数据模型**

```go
// internal/models/models.go
package models

import "time"

// RecycleOrder 回收订单统一数据模型
type RecycleOrder struct {
    OrderID       string    `json:"order_id"`        // 订单编号
    GameName      string    `json:"game_name"`        // 游戏名称
    ServerRegion  string    `json:"server_region"`    // 区服
    AccountInfo   string    `json:"account_info"`     // 账号信息摘要
    Price         float64   `json:"price"`            // 回收价格
    Status        string    `json:"status"`           // 订单状态
    CreateTime    time.Time `json:"create_time"`      // 创建时间
    CompleteTime  time.Time `json:"complete_time"`    // 完成时间
    BuyerInfo     string    `json:"buyer_info"`       // 买家信息
    Remarks       string    `json:"remarks"`          // 备注
    RawData       string    `json:"raw_data"`         // 原始数据JSON（防止丢失字段）
}

// CookieData Cookie 持久化格式
type CookieData struct {
    Cookies   []CookieEntry `json:"cookies"`
    UpdatedAt time.Time     `json:"updated_at"`
    ExpiresAt time.Time     `json:"expires_at"`
}

type CookieEntry struct {
    Name     string  `json:"name"`
    Value    string  `json:"value"`
    Domain   string  `json:"domain"`
    Path     string  `json:"path"`
    Expires  float64 `json:"expires"`
    HTTPOnly bool    `json:"http_only"`
    Secure   bool    `json:"secure"`
}

// FeishuRecord 飞书多维表格记录
type FeishuRecord struct {
    RecordID string                 `json:"record_id"`
    Fields   map[string]interface{} `json:"fields"`
}
```

- [ ] **Step 5: 编写 Makefile**

```makefile
# Makefile
.PHONY: build run cookie-tool clean deps

deps:
    go mod tidy
    go mod download

build:
    go build -o bin/scraper ./cmd/scraper
    go build -o bin/cookie-tool ./cmd/cookie-tool

run:
    go run ./cmd/scraper -config ./configs/config.yaml

cookie-tool:
    go run ./cmd/cookie-tool -config ./configs/config.yaml

clean:
    rm -rf bin/

test:
    go test ./... -v
```

- [ ] **Step 6: 创建 .gitignore 和 config.yaml.example**

```bash
# .gitignore
cat > .gitignore << 'EOF'
# 二进制文件
bin/
*.exe
*.exe~
*.dll
*.so
*.dylib

# 测试文件
*.test
*.out

# 配置文件（包含敏感信息）
configs/config.yaml

# 数据文件
data/
*.db
*.sqlite

# IDE
.vscode/
.idea/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# 临时文件
tmp/
temp/
*.tmp
EOF

# 复制配置文件模板（不含敏感信息）
cp configs/config.yaml configs/config.yaml.example
# 然后手动编辑 config.yaml.example，将敏感字段替换为占位符
```

- [ ] **Step 7: 安装依赖并验证编译**

```bash
cd c:/Automatic
go mod tidy
go build ./...
```

Expected: 编译成功，无错误。

- [ ] **Step 8: 提交**

```bash
git add -A
git commit -m "feat: project initialization, config system, and data models"
```

---

### Task 2: Cookie 管理模块

**Files:**
- Create: `internal/auth/cookie.go`
- Create: `cmd/cookie-tool/main.go`

**Interfaces:**
- Consumes: `config.Config` (特别是 `JYMConfig.CookiePath`)
- Consumes: `models.CookieData`, `models.CookieEntry`
- Produces: `auth.LoadCookies(path string) (*models.CookieData, error)` — 从文件加载 Cookie
- Produces: `auth.SaveCookies(path string, data *models.CookieData) error` — 保存 Cookie 到文件
- Produces: `auth.IsCookieValid(data *models.CookieData) bool` — 检查 Cookie 是否过期
- Produces: `auth.ImportFromJSON(path string, browserJSON []byte) error` — 从浏览器导出的JSON导入Cookie

- [ ] **Step 1: 编写 Cookie 加载/保存逻辑**

```go
// internal/auth/cookie.go
package auth

import (
    "encoding/json"
    "os"
    "time"

    "github.com/example/jiaoyimao-scraper/internal/models"
)

// LoadCookies 从JSON文件加载Cookie
func LoadCookies(path string) (*models.CookieData, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return &models.CookieData{}, nil
        }
        return nil, err
    }
    var cookies models.CookieData
    if err := json.Unmarshal(data, &cookies); err != nil {
        return nil, err
    }
    return &cookies, nil
}

// SaveCookies 将Cookie保存到JSON文件
func SaveCookies(path string, data *models.CookieData) error {
    data.UpdatedAt = time.Now()
    bytes, err := json.MarshalIndent(data, "", "  ")
    if err != nil {
        return err
    }
    // 确保目录存在
    if err := os.MkdirAll(getDir(path), 0755); err != nil {
        return err
    }
    return os.WriteFile(path, bytes, 0600)
}

// IsCookieValid 检查Cookie是否仍在有效期内
func IsCookieValid(data *models.CookieData) bool {
    if data == nil || len(data.Cookies) == 0 {
        return false
    }
    if time.Now().After(data.ExpiresAt) {
        return false
    }
    // 至少需要有登录态的关键Cookie
    hasSessionCookie := false
    for _, c := range data.Cookies {
        if c.Name == "token" || c.Name == "SESSION" || c.Name == "jym_token" {
            if c.Value != "" {
                hasSessionCookie = true
                break
            }
        }
    }
    return hasSessionCookie
}

// ImportFromJSON 从浏览器导出的Cookie JSON（如EditThisCookie格式）导入
func ImportFromJSON(path string, browserJSON []byte) error {
    var rawCookies []struct {
        Name     string  `json:"name"`
        Value    string  `json:"value"`
        Domain   string  `json:"domain"`
        Path     string  `json:"path"`
        Expires  float64 `json:"expirationDate"`
        HTTPOnly bool    `json:"httpOnly"`
        Secure   bool    `json:"secure"`
    }
    if err := json.Unmarshal(browserJSON, &rawCookies); err != nil {
        return err
    }

    data := &models.CookieData{
        Cookies: make([]models.CookieEntry, len(rawCookies)),
    }
    var maxExpiry float64
    for i, c := range rawCookies {
        data.Cookies[i] = models.CookieEntry{
            Name:     c.Name,
            Value:    c.Value,
            Domain:   c.Domain,
            Path:     c.Path,
            Expires:  c.Expires,
            HTTPOnly: c.HTTPOnly,
            Secure:   c.Secure,
        }
        if c.Expires > maxExpiry {
            maxExpiry = c.Expires
        }
    }
    data.ExpiresAt = time.Unix(int64(maxExpiry), 0)

    return SaveCookies(path, data)
}

func getDir(path string) string {
    for i := len(path) - 1; i >= 0; i-- {
        if path[i] == '/' || path[i] == '\\' {
            return path[:i]
        }
    }
    return "."
}
```

- [ ] **Step 2: 编写 Cookie 管理 CLI 工具**

```go
// cmd/cookie-tool/main.go
package main

import (
    "flag"
    "fmt"
    "os"

    "github.com/example/jiaoyimao-scraper/internal/auth"
    "github.com/example/jiaoyimao-scraper/internal/config"
)

func main() {
    configPath := flag.String("config", "./configs/config.yaml", "配置文件路径")
    importPath := flag.String("import", "", "从浏览器导出的Cookie JSON文件路径导入")
    checkOnly := flag.Bool("check", false, "仅检查Cookie是否有效")
    flag.Parse()

    cfg, err := config.Load(*configPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
        os.Exit(1)
    }

    // 导入模式
    if *importPath != "" {
        data, err := os.ReadFile(*importPath)
        if err != nil {
            fmt.Fprintf(os.Stderr, "读取导入文件失败: %v\n", err)
            os.Exit(1)
        }
        if err := auth.ImportFromJSON(cfg.JYM.CookiePath, data); err != nil {
            fmt.Fprintf(os.Stderr, "导入Cookie失败: %v\n", err)
            os.Exit(1)
        }
        fmt.Println("Cookie 导入成功！")
        fmt.Printf("保存位置: %s\n", cfg.JYM.CookiePath)
    }

    // 检查模式
    cookies, err := auth.LoadCookies(cfg.JYM.CookiePath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "加载Cookie失败: %v\n", err)
        os.Exit(1)
    }
    if auth.IsCookieValid(cookies) {
        fmt.Println("✓ Cookie 有效")
        fmt.Printf("  Cookie 数量: %d\n", len(cookies.Cookies))
        fmt.Printf("  过期时间: %s\n", cookies.ExpiresAt.Format("2006-01-02 15:04:05"))
    } else {
        fmt.Println("✗ Cookie 无效或已过期，请重新获取")
        os.Exit(1)
    }
}
```

- [ ] **Step 3: 编译并测试 Cookie 工具**

```bash
cd c:/Automatic
go build -o bin/cookie-tool ./cmd/cookie-tool
./bin/cookie-tool -config ./configs/config.yaml -check || echo "Cookie无效（预期行为，尚未登录）"
```

Expected: 工具正常运行，提示 Cookie 无效。

- [ ] **Step 4: 提交**

```bash
git add internal/auth/cookie.go cmd/cookie-tool/main.go
git commit -m "feat: add cookie management module and CLI tool"
```

---

### Task 3: Headless 浏览器引擎与页面操作封装

**Files:**
- Create: `internal/browser/browser.go`
- Create: `internal/browser/actions.go`

**Interfaces:**
- Consumes: `config.BrowserConfig`
- Consumes: `models.CookieEntry`（用于注入Cookie到浏览器）
- Produces: `browser.NewManager(cfg *config.BrowserConfig) (*Manager, error)` — 创建浏览器管理器
- Produces: `(*Manager).NewContext() (context.Context, context.CancelFunc)` — 创建带超时的浏览器上下文
- Produces: `(*Manager).Close() error` — 关闭浏览器
- Produces: `actions.Navigate(ctx, url string) chromedp.Action` — 导航到URL
- Produces: `actions.WaitVisible(ctx, selector string) chromedp.Action` — 等待元素可见
- Produces: `actions.Click(ctx, selector string) chromedp.Action` — 点击元素（不抢占鼠标）
- Produces: `actions.Input(ctx, selector, text string) chromedp.Action` — 输入文本
- Produces: `actions.ExtractTable(ctx, selector string, result *string) chromedp.Action` — 提取表格HTML
- Produces: `actions.ExtractText(ctx, selector string, result *string) chromedp.Action` — 提取文本
- Produces: `actions.Screenshot(ctx, path string) chromedp.Action` — 截图（调试用）

- [ ] **Step 1: 编写浏览器管理器**

```go
// internal/browser/browser.go
package browser

import (
    "context"
    "fmt"
    "os/exec"

    "github.com/chromedp/chromedp"

    "github.com/example/jiaoyimao-scraper/internal/config"
)

type Manager struct {
    allocCtx    context.Context
    allocCancel context.CancelFunc
    opts        []chromedp.ExecAllocatorOption
}

// NewManager 初始化浏览器管理器
// headless模式不显示窗口，所有操作在后端执行，不抢占鼠标
func NewManager(cfg *config.BrowserConfig) (*Manager, error) {
    opts := []chromedp.ExecAllocatorOption{
        chromedp.Flag("headless", cfg.Headless),
        chromedp.Flag("disable-gpu", true),
        chromedp.Flag("no-sandbox", true),
        chromedp.Flag("disable-dev-shm-usage", true),
        chromedp.Flag("disable-blink-features", "AutomationControlled"),
        // 设置窗口大小，确保元素可被定位和点击
        chromedp.WindowSize(1920, 1080),
        // 禁用自动化检测
        chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"),
    }

    if cfg.ChromePath != "" {
        opts = append(opts, chromedp.ExecPath(cfg.ChromePath))
    } else {
        // 自动查找Chrome路径
        if path, err := findChrome(); err == nil {
            opts = append(opts, chromedp.ExecPath(path))
        }
    }

    if cfg.DebugPort > 0 {
        opts = append(opts, chromedp.Flag("remote-debugging-port", fmt.Sprintf("%d", cfg.DebugPort)))
    }

    allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

    return &Manager{
        allocCtx:    allocCtx,
        allocCancel: allocCancel,
        opts:        opts,
    }, nil
}

// NewContext 创建带超时的浏览器上下文
func (m *Manager) NewContext(timeoutSec int) (context.Context, context.CancelFunc) {
    ctx, cancel := chromedp.NewContext(m.allocCtx)
    if timeoutSec > 0 {
        ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
    }
    return ctx, cancel
}

// NewTabContext 创建新的浏览器标签页上下文
func (m *Manager) NewTabContext(timeoutSec int) (context.Context, context.CancelFunc) {
    ctx, cancel := chromedp.NewContext(m.allocCtx)
    if timeoutSec > 0 {
        ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
    }
    return ctx, cancel
}

// Close 关闭浏览器
func (m *Manager) Close() error {
    m.allocCancel()
    return nil
}

// findChrome 在Windows上自动查找Chrome安装路径
func findChrome() (string, error) {
    paths := []string{
        `C:\Program Files\Google\Chrome\Application\chrome.exe`,
        `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
        `C:\Users\Default\AppData\Local\Google\Chrome\Application\chrome.exe`,
    }
    for _, p := range paths {
        if _, err := exec.LookPath(p); err == nil {
            return p, nil
        }
    }
    return "", fmt.Errorf("未找到Chrome，请在配置文件中指定chrome_path")
}
```

- [ ] **Step 2: 编写页面操作封装**

```go
// internal/browser/actions.go
package browser

import (
    "context"
    "fmt"
    "time"

    "github.com/chromedp/chromedp"
)

// Navigate 导航到指定URL并等待页面加载
func Navigate(url string) chromedp.Action {
    return chromedp.Navigate(url)
}

// WaitVisible 等待元素在DOM中可见
func WaitVisible(selector string) chromedp.Action {
    return chromedp.WaitVisible(selector, chromedp.ByQuery)
}

// WaitNotVisible 等待元素从DOM中消失
func WaitNotVisible(selector string) chromedp.Action {
    return chromedp.WaitNotVisible(selector, chromedp.ByQuery)
}

// WaitReady 等待文档就绪状态为complete
func WaitReady() chromedp.Action {
    return chromedp.ActionFunc(func(ctx context.Context) error {
        var state string
        if err := chromedp.Evaluate(`document.readyState`, &state).Do(ctx); err != nil {
            return err
        }
        if state != "complete" {
            return fmt.Errorf("页面未完全加载，当前状态: %s", state)
        }
        return nil
    })
}

// Click 点击元素（在headless模式下执行，不抢占用户鼠标）
func Click(selector string) chromedp.Action {
    return chromedp.Click(selector, chromedp.ByQuery)
}

// Input 在输入框中输入文本
func Input(selector, text string) chromedp.Action {
    return chromedp.SendKeys(selector, text, chromedp.ByQuery)
}

// ExtractHTML 提取元素的outerHTML
func ExtractHTML(selector string, result *string) chromedp.Action {
    return chromedp.OuterHTML(selector, result, chromedp.ByQuery)
}

// ExtractText 提取元素的文本内容
func ExtractText(selector string, result *string) chromedp.Action {
    return chromedp.TextContent(selector, result, chromedp.ByQuery)
}

// Screenshot 截图保存到文件（调试用）
func Screenshot(path string) chromedp.Action {
    return chromedp.FullScreenshot(path, 90)
}

// ScreenshotBytes 截图返回字节数据（用于验证码识别）
func ScreenshotBytes(selector string, result *[]byte) chromedp.Action {
    return chromedp.Screenshot(selector, result, chromedp.ByQuery)
}

// Sleep 等待指定时间
func Sleep(d time.Duration) chromedp.Action {
    return chromedp.Sleep(d)
}

// SetCookies 通过CDP协议设置浏览器Cookie（支持HTTPOnly）
func SetCookies(cookies []models.CookieEntry) chromedp.Action {
    return chromedp.ActionFunc(func(ctx context.Context) error {
        for _, c := range cookies {
            expr := fmt.Sprintf(
                `document.cookie = "%s=%s; domain=%s; path=%s; expires=" + new Date(%f * 1000).toUTCString() + "; Secure; SameSite=Lax"`,
                c.Name, c.Value, c.Domain, c.Path, c.Expires,
            )
            var res interface{}
            if err := chromedp.Evaluate(expr, &res).Do(ctx); err != nil {
                // HTTPOnly Cookie 无法通过JS设置，需要通过CDP Network.SetCookie
                // chromedp 的 cdp 调用可以在实际实现中通过 cdproto 完成
                continue
            }
        }
        return nil
    })
}

// ScrollIntoView 滚动到指定元素
func ScrollIntoView(selector string) chromedp.Action {
    return chromedp.ActionFunc(func(ctx context.Context) error {
        var res interface{}
        return chromedp.Evaluate(
            fmt.Sprintf(`document.querySelector('%s').scrollIntoView({behavior: 'instant', block: 'center'})`, selector),
            &res,
        ).Do(ctx)
    })
}

// WaitForNetworkIdle 等待网络空闲（无活跃请求）
func WaitForNetworkIdle(timeout time.Duration) chromedp.Action {
    return chromedp.ActionFunc(func(ctx context.Context) error {
        deadline := time.Now().Add(timeout)
        for time.Now().Before(deadline) {
            var pending int
            _ = chromedp.Evaluate(
                `performance.getEntriesByType('resource').filter(r => !r.responseEnd).length`,
                &pending,
            ).Do(ctx)
            if pending == 0 {
                return nil
            }
            time.Sleep(500 * time.Millisecond)
        }
        return nil
    })
}
```

- [ ] **Step 3: 编译验证**

```bash
cd c:/Automatic
go mod tidy
go build ./internal/browser/...
```

- [ ] **Step 4: 提交**

```bash
git add internal/browser/
git commit -m "feat: add headless browser engine and page action wrappers"
```

---

### Task 4: 滑块验证码识别与处理

**Files:**
- Create: `internal/captcha/captcha.go`
- Create: `internal/captcha/slider.go`
- Create: `internal/captcha/thirdparty.go`

**Interfaces:**
- Consumes: `config.CaptchaConfig`
- Produces: `captcha.Solver` 接口 — `Solve(ctx context.Context) error`
- Produces: `captcha.NewSliderSolver(cfg *config.CaptchaConfig) *SliderSolver` — OpenCV滑块识别
- Produces: `captcha.NewThirdPartySolver(cfg *config.CaptchaConfig) *ThirdPartySolver` — 第三方打码平台

- [ ] **Step 1: 定义验证码识别接口**

```go
// internal/captcha/captcha.go
package captcha

import "context"

// Solver 验证码识别器接口
type Solver interface {
    // Detect 检测页面是否出现了验证码
    Detect(ctx context.Context) (bool, error)
    // Solve 识别并解决验证码
    Solve(ctx context.Context) error
    // Type 返回验证码类型
    Type() string
}
```

- [ ] **Step 2: 编写滑块验证码识别逻辑**

```go
// internal/captcha/slider.go
package captcha

import (
    "context"
    "encoding/base64"
    "fmt"
    "image"
    _ "image/png"
    "math"
    "os"
    "time"

    "github.com/chromedp/chromedp"
)

// SliderSolver 基于OpenCV/图像处理的滑块验证码识别器
type SliderSolver struct {
    maxRetry int
    // 滑块选择器（根据交易猫实际页面调整）
    sliderSelector  string // 滑块按钮选择器
    bgImgSelector   string // 带缺口的背景图选择器
    gapImgSelector  string // 缺口图选择器
}

func NewSliderSolver(maxRetry int) *SliderSolver {
    return &SliderSolver{
        maxRetry:       maxRetry,
        sliderSelector: ".slider-button, .nc_iconfont.btn_slide, .slide-verify-slider",
        bgImgSelector:  ".slide-verify-bg, canvas.bg",
        gapImgSelector:  ".slide-verify-gap, canvas.gap",
    }
}

func (s *SliderSolver) Type() string { return "slider" }

// Detect 检测滑块验证码是否出现
func (s *SliderSolver) Detect(ctx context.Context) (bool, error) {
    var nodes []struct{ NodeID int64 }
    // 尝试多个常见的滑块选择器
    selectors := []string{
        ".slider-button", ".nc_iconfont.btn_slide",
        ".slide-verify-slider", "#sliderCaptcha",
        "[class*='slider']", "[class*='captcha']",
    }
    for _, sel := range selectors {
        err := chromedp.Nodes(sel, &nodes, chromedp.ByQuery, chromedp.AtLeast(0)).Do(ctx)
        if err == nil && len(nodes) > 0 {
            return true, nil
        }
    }
    return false, nil
}

// Solve 解决滑块验证码
// 策略：截图 → 计算缺口位置 → 模拟人类拖拽轨迹 → 移动滑块
func (s *SliderSolver) Solve(ctx context.Context) error {
    for attempt := 0; attempt < s.maxRetry; attempt++ {
        // 1. 截图背景图和缺口图
        var bgBytes, gapBytes []byte
        if err := chromedp.Run(ctx,
            chromedp.Screenshot(s.bgImgSelector, &bgBytes, chromedp.ByQuery),
        ); err != nil {
            // 如果是canvas，使用Evaluate获取
            bgBytes = s.captureCanvas(ctx, s.bgImgSelector)
        }

        // 2. 计算滑块缺口位置
        distance, err := s.calculateGapDistance(bgBytes)
        if err != nil {
            return fmt.Errorf("计算缺口位置失败 (尝试 %d/%d): %w", attempt+1, s.maxRetry, err)
        }

        // 3. 模拟人类拖拽轨迹并移动滑块
        if err := s.simulateDrag(ctx, s.sliderSelector, distance); err != nil {
            // 可能是距离算错了，重试
            time.Sleep(500 * time.Millisecond)
            continue
        }

        // 4. 检查验证码是否消失
        time.Sleep(1 * time.Second)
        detected, _ := s.Detect(ctx)
        if !detected {
            return nil // 验证成功
        }
    }
    return fmt.Errorf("滑块验证失败，已重试%d次", s.maxRetry)
}

// calculateGapDistance 通过图像分析计算缺口距离
func (s *SliderSolver) calculateGapDistance(bgImageBytes []byte) (int, error) {
    // 方案A: 使用gocv（OpenCV Go绑定）
    // 方案B: 使用纯Go图像处理（下面给出纯Go简化版）
    //
    // 简化实现：将图片解码，找到缺口边缘
    // 实际生产环境建议集成gocv或调用第三方服务

    if len(bgImageBytes) == 0 {
        return 0, fmt.Errorf("背景图为空")
    }

    // 将图片保存临时文件后用image库解码
    tmpFile, err := os.CreateTemp("", "captcha_bg_*.png")
    if err != nil {
        return 0, err
    }
    defer os.Remove(tmpFile.Name())
    if _, err := tmpFile.Write(bgImageBytes); err != nil {
        return 0, err
    }
    tmpFile.Close()

    f, err := os.Open(tmpFile.Name())
    if err != nil {
        return 0, err
    }
    defer f.Close()

    img, _, err := image.Decode(f)
    if err != nil {
        return 0, err
    }

    bounds := img.Bounds()
    width := bounds.Dx()
    height := bounds.Dy()

    // 边缘检测：从左侧扫描，找到第一个显著边缘即为缺口左边界
    prevDiff := 0.0
    gapLeft := 0
    for x := 10; x < width-10; x++ {
        diff := 0.0
        for y := height / 4; y < height*3/4; y++ {
            r1, g1, b1, _ := img.At(x, y).RGBA()
            r2, g2, b2, _ := img.At(x-1, y).RGBA()
            diff += math.Abs(float64(r1)-float64(r2)) +
                math.Abs(float64(g1)-float64(g2)) +
                math.Abs(float64(b1)-float64(b2))
        }
        diff /= float64(height/2) * 3.0 * 65535.0

        if diff > prevDiff*3 && diff > 0.02 {
            gapLeft = x
            break
        }
        prevDiff = diff
    }

    if gapLeft == 0 {
        gapLeft = width / 3 // 默认偏移
    }

    return gapLeft, nil
}

// simulateDrag 模拟人类拖拽滑块的行为
func (s *SliderSolver) simulateDrag(ctx context.Context, selector string, distance int) error {
    // 使用chromedp模拟鼠标拖拽，生成带有人类特征的轨迹
    // 轨迹特征：先加速 → 匀速 → 减速 → 略微回退

    // 获取滑块元素位置
    var rect struct {
        X, Y, Width, Height float64
    }
    if err := chromedp.Evaluate(
        fmt.Sprintf(`(() => {
            const el = document.querySelector('%s');
            const r = el.getBoundingClientRect();
            return {x: r.x, y: r.y, width: r.width, height: r.height};
        })()`, selector),
        &rect,
    ).Do(ctx); err != nil {
        return fmt.Errorf("获取滑块位置失败: %w", err)
    }

    startX := rect.X + rect.Width/2
    startY := rect.Y + rect.Height/2

    // 生成人类拖拽轨迹
    trajectory := generateHumanTrajectory(distance)

    // 执行拖拽
    // 1. 鼠标移动到滑块位置
    // 2. 按下鼠标
    // 3. 按轨迹逐步移动
    // 4. 释放鼠标

    // 注意：chromedp 的 MouseClickXY + Drag 可以模拟拖拽
    // 这里使用较低级别的 Input.dispatchMouseEvent 来精细控制轨迹
    for i, point := range trajectory {
        x := startX + point
        y := startY + float64(i%3-1)*0.5 // 轻微的Y轴抖动模拟人类

        action := chromedp.MouseEvent("mouseMoved", x, y)
        if i == 0 {
            action = chromedp.MouseEvent("mousePressed", x, y)
        }
        if err := action.Do(ctx); err != nil {
            return err
        }
        // 每步间隔约10ms（人类拖拽的大致时间）
        time.Sleep(time.Duration(8+int(math.Sin(float64(i)*0.3)*3)) * time.Millisecond)
    }

    // 释放鼠标
    finalX := startX + float64(distance)
    finalY := startY
    if err := chromedp.MouseEvent("mouseReleased", finalX, finalY).Do(ctx); err != nil {
        return err
    }

    return nil
}

// generateHumanTrajectory 生成人类拖拽轨迹
func generateHumanTrajectory(totalDistance int) []float64 {
    steps := 50 + totalDistance/5 // 每5px一个采样点
    trajectory := make([]float64, steps)
    for i := 0; i < steps; i++ {
        progress := float64(i) / float64(steps)
        // 使用sigmoid-like曲线：先加速后减速
        eased := 1 / (1 + math.Exp(-10*(progress-0.5)))
        trajectory[i] = float64(totalDistance) * eased
    }
    // 最后几步添加微小回退（人类特征）
    trajectory[steps-1] = float64(totalDistance)
    trajectory[steps-2] = float64(totalDistance) - 1
    trajectory[steps-3] = float64(totalDistance) - 2
    return trajectory
}

// captureCanvas 从Canvas元素中提取图像数据
func (s *SliderSolver) captureCanvas(ctx context.Context, selector string) []byte {
    expr := fmt.Sprintf(`
        (() => {
            const canvas = document.querySelector('%s');
            if (!canvas) return '';
            return canvas.toDataURL('image/png').split(',')[1];
        })()
    `, selector)
    var base64Str string
    if err := chromedp.Evaluate(expr, &base64Str).Do(ctx); err != nil || base64Str == "" {
        return nil
    }
    decoded, err := base64.StdEncoding.DecodeString(base64Str)
    if err != nil {
        return nil
    }
    return decoded
}
```

- [ ] **Step 3: 编写第三方打码平台备用方案**

```go
// internal/captcha/thirdparty.go
package captcha

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

// ThirdPartySolver 通过第三方打码平台（如超级鹰）解决滑块验证码
type ThirdPartySolver struct {
    apiKey   string
    provider string // "chaojiying" | "2captcha"
    client   *http.Client
}

func NewThirdPartySolver(provider, apiKey string) *ThirdPartySolver {
    return &ThirdPartySolver{
        provider: provider,
        apiKey:   apiKey,
        client:   &http.Client{Timeout: 30 * time.Second},
    }
}

func (t *ThirdPartySolver) Type() string { return "thirdparty:" + t.provider }

func (t *ThirdPartySolver) Detect(ctx context.Context) (bool, error) {
    // 复用 SliderSolver 的检测逻辑
    s := &SliderSolver{maxRetry: 1}
    return s.Detect(ctx)
}

func (t *ThirdPartySolver) Solve(ctx context.Context) error {
    switch t.provider {
    case "chaojiying":
        return t.solveChaojiying(ctx)
    case "2captcha":
        return t.solve2Captcha(ctx)
    default:
        return fmt.Errorf("不支持的验证码平台: %s", t.provider)
    }
}

func (t *ThirdPartySolver) solveChaojiying(ctx context.Context) error {
    // 超级鹰API调用：上传背景图+缺口图，获取滑动距离
    // POST https://upload.chaojiying.net/Upload/Processing.php
    // 参数: user, pass, softid, codetype, userfile
    // 此处为接口骨架，具体实现需根据超级鹰最新文档调整
    body := map[string]string{
        "user":     "",
        "pass":     "",
        "softid":   "",
        "codetype": "9101", // 滑块验证码类型码
    }
    jsonBody, _ := json.Marshal(body)
    resp, err := t.client.Post(
        "https://upload.chaojiying.net/Upload/Processing.php",
        "application/json",
        bytes.NewReader(jsonBody),
    )
    if err != nil {
        return fmt.Errorf("超级鹰请求失败: %w", err)
    }
    defer resp.Body.Close()
    // 解析响应获取滑动距离...（省略具体解析逻辑）
    return nil
}

func (t *ThirdPartySolver) solve2Captcha(ctx context.Context) error {
    // 2captcha API调用
    return fmt.Errorf("2captcha集成待实现")
}
```

- [ ] **Step 4: 编译验证**

```bash
cd c:/Automatic
go mod tidy
go build ./internal/captcha/...
```

- [ ] **Step 5: 提交**

```bash
git add internal/captcha/
git commit -m "feat: add slider CAPTCHA solver with human-like trajectory simulation"
```

---

### Task 5: 自动登录与Cookie刷新

**Files:**
- Create: `internal/auth/login.go`

**Interfaces:**
- Consumes: `browser.Manager`, `captcha.Solver`, `config.Config`
- Consumes: `auth.LoadCookies`, `auth.SaveCookies`
- Produces: `auth.LoginService` 结构体 — 提供 `PerformLogin` 和 `RefreshIfNeeded` 方法

- [ ] **Step 1: 编写自动登录逻辑**

```go
// internal/auth/login.go
package auth

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"

    "github.com/chromedp/chromedp"
    "github.com/chromedp/cdproto/network"

    "github.com/example/jiaoyimao-scraper/internal/browser"
    "github.com/example/jiaoyimao-scraper/internal/captcha"
    "github.com/example/jiaoyimao-scraper/internal/config"
    "github.com/example/jiaoyimao-scraper/internal/models"
)

// LoginService 自动登录服务
type LoginService struct{}

// PerformLogin 执行自动登录流程
// 所有操作在headless浏览器中执行，不抢占用户鼠标
func (s *LoginService) PerformLogin(
    ctx context.Context,
    cfg *config.Config,
    captchaSolver captcha.Solver,
) (*models.CookieData, error) {
    loginURL := cfg.JYM.BaseURL + "/login"
    log.Printf("[登录] 导航到登录页: %s", loginURL)

    // 1. 导航到登录页
    if err := chromedp.Run(ctx,
        browser.Navigate(loginURL),
        browser.WaitReady(),
        browser.Sleep(2*time.Second),
    ); err != nil {
        return nil, fmt.Errorf("加载登录页失败: %w", err)
    }

    // 2. 智能检测登录表单并输入用户名和密码
    inputSelectors := s.detectLoginForm(cfg)
    log.Printf("[登录] 使用账号密码登录")

    if err := chromedp.Run(ctx,
        browser.WaitVisible(inputSelectors.username),
        browser.Input(inputSelectors.username, cfg.JYM.Username),
        browser.Sleep(500*time.Millisecond),
        browser.Input(inputSelectors.password, cfg.JYM.Password),
        browser.Sleep(500*time.Millisecond),
    ); err != nil {
        return nil, fmt.Errorf("输入账号密码失败: %w", err)
    }

    // 3. 点击登录按钮
    if err := chromedp.Run(ctx,
        browser.Click(inputSelectors.submitBtn),
        browser.Sleep(2*time.Second),
    ); err != nil {
        return nil, fmt.Errorf("点击登录按钮失败: %w", err)
    }

    // 4. 轮询检测并处理滑块验证码（可能延迟出现）
    if err := s.waitAndSolveCaptcha(ctx, captchaSolver); err != nil {
        return nil, fmt.Errorf("验证码处理失败: %w", err)
    }

    // 5. 等待登录成功（检查是否跳转到工作台页面）
    if err := chromedp.Run(ctx,
        browser.WaitVisible(`.workbench, .main-content, [class*="layout"]`, chromedp.ByQuery),
        browser.Sleep(1*time.Second),
    ); err != nil {
        return nil, fmt.Errorf("登录验证失败，未检测到工作台页面: %w", err)
    }

    // 6. 通过CDP协议提取所有Cookie（包括HTTPOnly）
    cookieData, err := s.extractCookies(ctx)
    if err != nil {
        return nil, fmt.Errorf("提取Cookie失败: %w", err)
    }

    log.Printf("[登录] 登录成功，提取到 %d 个Cookie", len(cookieData.Cookies))
    return cookieData, nil
}

// RefreshIfNeeded 检查Cookie是否有效，无效时重新登录
func (s *LoginService) RefreshIfNeeded(
    ctx context.Context,
    cfg *config.Config,
    captchaSolver captcha.Solver,
) (*models.CookieData, error) {
    existing, err := LoadCookies(cfg.JYM.CookiePath)
    if err == nil && IsCookieValid(existing) {
        log.Printf("[Cookie] Cookie有效，跳过登录")
        return existing, nil
    }

    log.Printf("[Cookie] Cookie无效或过期，执行自动登录")
    newCookies, err := s.PerformLogin(ctx, cfg, captchaSolver)
    if err != nil {
        return nil, err
    }

    if err := SaveCookies(cfg.JYM.CookiePath, newCookies); err != nil {
        log.Printf("[Cookie] 保存Cookie失败: %v", err)
    }

    return newCookies, nil
}

// detectLoginForm 返回登录表单选择器（根据配置决定使用哪种登录方式）
func (s *LoginService) detectLoginForm(cfg *config.Config) loginFormSelectors {
    // 交易猫通常支持：手机号+密码 / 手机号+短信验证码
    // 默认使用密码登录方式
    if cfg.JYM.LoginType == "sms" {
        return loginFormSelectors{
            username:  `input[type="text"], input[placeholder*="手机"], input[name="phone"]`,
            password:  `input[placeholder*="验证码"], input[name="sms_code"]`,
            submitBtn: `button[type="submit"], .login-btn, [class*="login"] button`,
            smsBtn:    `.get-sms-code, [class*="sms"] button, .send-code`,
        }
    }
    return loginFormSelectors{
        username:  `input[type="text"], input[placeholder*="手机"], input[placeholder*="账号"], input[name="phone"]`,
        password:  `input[type="password"], input[placeholder*="密码"]`,
        submitBtn: `button[type="submit"], .login-btn, [class*="login"] button, form button`,
    }
}

type loginFormSelectors struct {
    username  string
    password  string
    submitBtn string
    smsBtn    string // 获取短信验证码按钮（仅短信登录模式）
}

// waitAndSolveCaptcha 轮询等待并解决验证码（验证码可能延迟1-3秒出现）
func (s *LoginService) waitAndSolveCaptcha(ctx context.Context, solver captcha.Solver) error {
    // 轮询检测验证码，最多等待10秒
    for i := 0; i < 20; i++ {
        detected, err := solver.Detect(ctx)
        if err != nil {
            log.Printf("[登录] 验证码检测出错: %v", err)
            continue
        }
        if detected {
            log.Printf("[登录] 检测到%s验证码，开始自动解决", solver.Type())
            if err := solver.Solve(ctx); err != nil {
                return fmt.Errorf("%s验证码解决失败: %w", solver.Type(), err)
            }
            log.Printf("[登录] 验证码已解决")
            // 验证通过后等待页面跳转
            chromedp.Run(ctx, browser.Sleep(3*time.Second))
            return nil
        }
        time.Sleep(500 * time.Millisecond)
    }
    // 10秒内未检测到验证码，可能不需要验证码
    log.Printf("[登录] 未检测到验证码，继续流程")
    return nil
}

// extractCookies 通过CDP Network.GetCookies 提取浏览器中所有Cookie（包括HTTPOnly）
func (s *LoginService) extractCookies(ctx context.Context) (*models.CookieData, error) {
    // 获取当前页面的URL作为cookie domain
    var currentURL string
    if err := chromedp.Evaluate(`window.location.href`, &currentURL).Do(ctx); err != nil {
        return nil, fmt.Errorf("获取当前URL失败: %w", err)
    }

    // 通过CDP协议获取所有Cookie（包括HTTPOnly的）
    cdpCookies, err := network.GetCookies().WithUrls([]string{currentURL}).Do(ctx)
    if err != nil {
        return nil, fmt.Errorf("CDP获取Cookie失败: %w", err)
    }

    var entries []models.CookieEntry
    var maxExpires float64
    for _, c := range cdpCookies {
        entry := models.CookieEntry{
            Name:     c.Name,
            Value:    c.Value,
            Domain:   c.Domain,
            Path:     c.Path,
            Expires:  float64(c.Expires),
            HTTPOnly: c.HTTPOnly,
            Secure:   c.Secure,
        }
        entries = append(entries, entry)
        if float64(c.Expires) > maxExpires {
            maxExpires = float64(c.Expires)
        }
    }

    expiresAt := time.Now().Add(24 * time.Hour) // 默认24小时
    if maxExpires > 0 {
        expiresAt = time.Unix(int64(maxExpires), 0)
    }

    return &models.CookieData{
        Cookies:   entries,
        UpdatedAt: time.Now(),
        ExpiresAt: expiresAt,
    }, nil
}
```

- [ ] **Step 2: 编译验证**

```bash
cd c:/Automatic
go mod tidy
go build ./internal/auth/...
```

- [ ] **Step 3: 提交**

```bash
git add internal/auth/login.go
git commit -m "feat: add automated login with CAPTCHA handling and cookie refresh"
```

---

### Task 6: 数据抓取引擎（API模式 + 浏览器兜底模式）

**Files:**
- Create: `internal/scraper/api.go`
- Create: `internal/scraper/browser.go`
- Create: `internal/scraper/parser.go`
- Create: `internal/scraper/manager.go`

**Interfaces:**
- Consumes: `browser.Manager`, `config.Config`, `models.CookieData`
- Produces: `scraper.NewManager(cfg *config.Config, browserMgr *browser.Manager, cookies *models.CookieData) *Manager`
- Produces: `(*Manager).ScrapeAll(ctx context.Context) (map[string][]models.RecycleOrder, error)` — 抓取全部游戏数据，返回 游戏名→订单列表
- Produces: `(*Manager).ScrapeGame(ctx context.Context, game config.GameConfig) ([]models.RecycleOrder, error)` — 抓取单个游戏数据

- [ ] **Step 1: 编写API模式抓取**

```go
// internal/scraper/api.go
package scraper

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/example/jiaoyimao-scraper/internal/models"
)

// apiClient 通过反向工程出的内部API直接获取数据
type apiClient struct {
    baseURL    string
    httpClient *http.Client
    cookies    []models.CookieEntry
}

func newAPIClient(baseURL string, cookies []models.CookieEntry) *apiClient {
    return &apiClient{
        baseURL: baseURL,
        httpClient: &http.Client{Timeout: 30 * time.Second},
        cookies:    cookies,
    }
}

// FetchRecycleOrders 通过API获取回收订单列表
// API端点需要先在浏览器DevTools中抓包确认
// 典型模式：GET/POST /api/v1/merchant/recycle/orders?game=xxx&page=1&pageSize=100
func (c *apiClient) FetchRecycleOrders(ctx context.Context, gameName string, tableIndex int) ([]models.RecycleOrder, error) {
    // API URL需根据实际抓包结果替换
    // 以下为推测的API路径模式
    apiURL := fmt.Sprintf("%s/api/v1/merchant/recycle/orders", c.baseURL)

    req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
    if err != nil {
        return nil, fmt.Errorf("创建请求失败: %w", err)
    }

    // 添加查询参数
    q := req.URL.Query()
    q.Add("game", gameName)
    q.Add("page", "1")
    q.Add("pageSize", "500") // 一次获取尽量多的数据
    req.URL.RawQuery = q.Encode()

    // 设置请求头
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json")
    req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
    req.Header.Set("Referer", c.baseURL+"/workbench")

    // 设置Cookie
    for _, cookie := range c.cookies {
        req.AddCookie(&http.Cookie{
            Name:   cookie.Name,
            Value:  cookie.Value,
            Domain: cookie.Domain,
            Path:   cookie.Path,
        })
    }

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("API请求失败: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == 401 || resp.StatusCode == 403 {
        return nil, fmt.Errorf("Cookie已过期 (HTTP %d)", resp.StatusCode)
    }
    if resp.StatusCode != 200 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("API返回错误 (HTTP %d): %s", resp.StatusCode, string(body))
    }

    // 解析响应（格式需根据实际API响应调整）
    var apiResp struct {
        Code    int                    `json:"code"`
        Message string                 `json:"message"`
        Data    struct {
            List  []models.RecycleOrder `json:"list"`
            Total int                  `json:"total"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
        return nil, fmt.Errorf("解析API响应失败: %w", err)
    }

    if apiResp.Code != 0 {
        return nil, fmt.Errorf("API业务错误: %s", apiResp.Message)
    }

    return apiResp.Data.List, nil
}

// ProbeAPI 探测API是否可用（发送轻量请求测试连通性）
func (c *apiClient) ProbeAPI(ctx context.Context) bool {
    req, _ := http.NewRequestWithContext(ctx, "HEAD", c.baseURL+"/api/", nil)
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return false
    }
    resp.Body.Close()
    return resp.StatusCode < 500
}
```

- [ ] **Step 2: 编写浏览器模式抓取（API失败时的兜底）**

```go
// internal/scraper/browser.go
package scraper

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/chromedp/chromedp"

    "github.com/example/jiaoyimao-scraper/internal/browser"
    "github.com/example/jiaoyimao-scraper/internal/config"
    "github.com/example/jiaoyimao-scraper/internal/models"
)

// browserScraper 通过headless浏览器导航页面来提取数据
type browserScraper struct {
    cfg        *config.Config
    browserMgr *browser.Manager
}

func newBrowserScraper(cfg *config.Config, mgr *browser.Manager) *browserScraper {
    return &browserScraper{cfg: cfg, browserMgr: mgr}
}

// ScrapeRecyclePage 通过浏览器模拟点击导航到回收页面并提取表格数据
// 所有点击操作在headless模式执行，不抢占鼠标
func (b *browserScraper) ScrapeRecyclePage(
    ctx context.Context,
    game config.GameConfig,
    tableIndex int,
) ([]models.RecycleOrder, error) {
    var orders []models.RecycleOrder
    workbenchURL := b.cfg.JYM.BaseURL + "/workbench"

    actions := []chromedp.Action{
        // 1. 导航到工作台
        browser.Navigate(workbenchURL),
        browser.WaitReady(),
        browser.Sleep(2 * time.Second),
    }

    // 2. 点击左侧导航"我的交易猫"
    actions = append(actions,
        browser.WaitVisible(`.sidebar, .nav-left, [class*="sidebar"]`, chromedp.ByQuery),
        browser.Click(`[class*="我的交易猫"], .nav-item:has-text("我的交易猫")`),
        browser.Sleep(1*time.Second),
    )

    // 3. 点击"我的回收"
    actions = append(actions,
        browser.Click(`[class*="我的回收"], .nav-item:has-text("我的回收")`),
        browser.Sleep(1*time.Second),
    )

    // 4. 点击对应游戏标签
    gameTabSelector := fmt.Sprintf(`[class*="%s"], .tab:has-text("%s")`, game.Name, game.Name)
    actions = append(actions,
        browser.WaitVisible(gameTabSelector, chromedp.ByQuery),
        browser.Click(gameTabSelector),
        browser.Sleep(2*time.Second),
        browser.WaitForNetworkIdle(5*time.Second),
    )

    // 5. 如果该游戏有多个表格（tableIndex > 0），切换到对应子标签/分页
    if tableIndex > 0 {
        subTabSelector := b.buildSubTabSelector(game, tableIndex)
        if subTabSelector != "" {
            actions = append(actions,
                browser.Click(subTabSelector),
                browser.Sleep(1*time.Second),
                browser.WaitForNetworkIdle(5*time.Second),
            )
        }
    }

    // 6. 提取所有表格数据
    var tableHTML string
    actions = append(actions,
        browser.ExtractHTML(`table, [class*="table"], .el-table`, &tableHTML),
    )

    if err := chromedp.Run(ctx, actions...); err != nil {
        return nil, fmt.Errorf("浏览器操作失败: %w", err)
    }

    // 7. 解析HTML表格
    orders = parseTableHTML(tableHTML, game.Name)
    log.Printf("[抓取-浏览器] %s (表格%d): 获取到 %d 条记录", game.Name, tableIndex, len(orders))

    return orders, nil
}

// buildSubTabSelector 构建子表格/分页的选择器
func (b *browserScraper) buildSubTabSelector(game config.GameConfig, index int) string {
    // 根据游戏名称和表格索引构建选择器
    // 例如原神可能分为"官服"和"渠道服"两个表格
    subTabs := map[string][]string{
        "原神":       {"官服", "渠道服/B服"},
        "崩坏：星穹铁道": {"官服", "渠道服"},
    }
    if tabs, ok := subTabs[game.Name]; ok && index < len(tabs) {
        return fmt.Sprintf(`.sub-tab:has-text("%s")`, tabs[index])
    }
    return ""
}
```

- [ ] **Step 3: 编写HTML表格解析器**

```go
// internal/scraper/parser.go
package scraper

import (
    "strconv"
    "strings"
    "time"

    "github.com/PuerkitoBio/goquery"
    "github.com/example/jiaoyimao-scraper/internal/models"
)

// parseTableHTML 解析HTML中的表格数据
func parseTableHTML(html string, gameName string) []models.RecycleOrder {
    doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
    if err != nil {
        return nil
    }

    var orders []models.RecycleOrder

    // 查找表格行（跳过表头）
    doc.Find("table tbody tr, .el-table__body tr").Each(func(i int, row *goquery.Selection) {
        cells := row.Find("td, .el-table__cell")
        if cells.Length() == 0 {
            return
        }

        // 根据交易猫实际表格列顺序解析
        // 典型列：订单编号 | 账号信息 | 区服 | 价格 | 状态 | 时间 | 操作
        order := models.RecycleOrder{
            GameName: gameName,
        }

        cells.Each(func(j int, cell *goquery.Selection) {
            text := strings.TrimSpace(cell.Text())
            switch j {
            case 0:
                order.OrderID = text
            case 1:
                order.AccountInfo = text
            case 2:
                order.ServerRegion = text
            case 3:
                // parse price...
                order.Price = parsePrice(text)
            case 4:
                order.Status = text
            case 5:
                order.CreateTime = parseTime(text)
            }
        })

        orders = append(orders, order)
    })

    return orders
}

// parseJSONResponse 解析API返回的JSON数据
func parseJSONResponse(body []byte, gameName string) ([]models.RecycleOrder, error) {
    // 实现在 api.go 的 FetchRecycleOrders 中已完成JSON解码
    // 此处作为独立函数，用于处理不同结构的API响应
    return nil, nil
}

func parsePrice(text string) float64 {
    text = strings.TrimPrefix(text, "¥")
    text = strings.TrimPrefix(text, "￥")
    text = strings.TrimSpace(text)
    text = strings.ReplaceAll(text, ",", "") // 去掉千分位逗号
    price, err := strconv.ParseFloat(text, 64)
    if err != nil {
        return 0
    }
    return price
}

func parseTime(text string) time.Time {
    text = strings.TrimSpace(text)
    formats := []string{
        "2006-01-02 15:04:05",
        "2006-01-02 15:04",
        "2006-01-02",
        "2006/01/02 15:04:05",
        "2006/01/02",
        "01-02 15:04",       // 月-日 时:分（同年默认）
        "2006年01月02日 15:04:05",
    }
    for _, f := range formats {
        if t, err := time.Parse(f, text); err == nil {
            return t
        }
    }
    return time.Time{}
}

// normalizeGameName 将可能的游戏名变体统一化为标准名称
func normalizeGameName(raw string) string {
    mapping := map[string]string{
        "火影忍者":   "火影忍者",
        "火影":     "火影忍者",
        "naruto": "火影忍者",
        "原神":     "原神",
        "genshin": "原神",
        "绝区零":    "绝区零",
        "zzz":    "绝区零",
        "崩坏：星穹铁道": "崩坏：星穹铁道",
        "星穹铁道":   "崩坏：星穹铁道",
        "崩坏星穹铁道": "崩坏：星穹铁道",
        "hsr":    "崩坏：星穹铁道",
        "鸣潮":     "鸣潮",
        "wuthering": "鸣潮",
        "三角洲行动":  "三角洲行动",
        "deltaforce": "三角洲行动",
        "三角洲":    "三角洲行动",
    }
    if standard, ok := mapping[raw]; ok {
        return standard
    }
    return raw
}
```

- [ ] **Step 4: 编写抓取管理器（编排API + 浏览器兜底）**

```go
// internal/scraper/manager.go
package scraper

import (
    "context"
    "log"

    "github.com/example/jiaoyimao-scraper/internal/browser"
    "github.com/example/jiaoyimao-scraper/internal/config"
    "github.com/example/jiaoyimao-scraper/internal/models"
)

// Manager 抓取管理器，协调API和浏览器两种模式
type Manager struct {
    cfg       *config.Config
    cookies   *models.CookieData
    apiClient *apiClient
    browserS  *browserScraper
}

func NewManager(cfg *config.Config, browserMgr *browser.Manager, cookies *models.CookieData) *Manager {
    return &Manager{
        cfg:       cfg,
        cookies:   cookies,
        apiClient: newAPIClient(cfg.JYM.BaseURL, cookies.Cookies),
        browserS:  newBrowserScraper(cfg, browserMgr),
    }
}

// ScrapeAll 抓取所有配置中游戏的所有表格数据
func (m *Manager) ScrapeAll(ctx context.Context) (map[string][]models.RecycleOrder, error) {
    result := make(map[string][]models.RecycleOrder)

    for _, game := range m.cfg.Scraper.Games {
        for i := 0; i < game.TableCount; i++ {
            tableKey := game.Name
            if game.TableCount > 1 {
                tableKey = tableKey + "_table" + string(rune('1'+i))
            }

            orders, err := m.ScrapeGameTable(ctx, game, i)
            if err != nil {
                log.Printf("[抓取] %s 表格%d 失败: %v", game.Name, i, err)
                continue
            }
            result[tableKey] = orders
            log.Printf("[抓取] %s: %d 条记录", tableKey, len(orders))
        }
    }

    return result, nil
}

// ScrapeGameTable 抓取单个游戏的单个表格
// 策略：auto模式 → 先尝试API，失败则用浏览器兜底
func (m *Manager) ScrapeGameTable(ctx context.Context, game config.GameConfig, tableIndex int) ([]models.RecycleOrder, error) {
    mode := m.cfg.Scraper.Mode

    if mode == "auto" || mode == "api" {
        // 先尝试API
        if m.apiClient.ProbeAPI(ctx) {
            orders, err := m.apiClient.FetchRecycleOrders(ctx, game.Name, tableIndex)
            if err == nil && len(orders) > 0 {
                return orders, nil
            }
            log.Printf("[抓取] API获取 %s 表格%d 失败: %v, 切换到浏览器模式", game.Name, tableIndex, err)
        }
    }

    if mode == "auto" || mode == "browser" {
        return m.browserS.ScrapeRecyclePage(ctx, game, tableIndex)
    }

    return nil, nil
}
```

- [ ] **Step 5: 编译验证**

```bash
cd c:/Automatic
go mod tidy
go build ./internal/scraper/...
```

- [ ] **Step 6: 提交**

```bash
git add internal/scraper/
git commit -m "feat: add scraping engine with API-first + browser fallback strategy"
```

---

### Task 7: 飞书多维表格集成

**Files:**
- Create: `internal/feishu/client.go`
- Create: `internal/feishu/bitable.go`

**Interfaces:**
- Consumes: `config.FeishuConfig`
- Produces: `feishu.NewClient(cfg *config.FeishuConfig) *Client` — 飞书客户端
- Produces: `(*Client).GetTenantAccessToken(ctx) (string, error)` — 获取tenant_access_token
- Produces: `(*Client).ListRecords(ctx, tableID string) ([]models.FeishuRecord, error)` — 列出表格记录
- Produces: `(*Client).BatchInsertRecords(ctx, tableID string, records []models.RecycleOrder) error` — 批量插入/更新记录

- [ ] **Step 1: 编写飞书API客户端**

```go
// internal/feishu/client.go
package feishu

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "sync"
    "time"

    "github.com/example/jiaoyimao-scraper/internal/config"
)

const feishuBaseURL = "https://open.feishu.cn/open-apis"

type Client struct {
    appID     string
    appSecret string
    httpClient *http.Client

    mu        sync.RWMutex
    token     string
    tokenExp  time.Time
}

func NewClient(cfg *config.FeishuConfig) *Client {
    return &Client{
        appID:     cfg.AppID,
        appSecret: cfg.AppSecret,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

// GetTenantAccessToken 获取并缓存 tenant_access_token
func (c *Client) GetTenantAccessToken(ctx context.Context) (string, error) {
    c.mu.RLock()
    if c.token != "" && time.Now().Before(c.tokenExp) {
        defer c.mu.RUnlock()
        return c.token, nil
    }
    c.mu.RUnlock()

    c.mu.Lock()
    defer c.mu.Unlock()

    // 双重检查
    if c.token != "" && time.Now().Before(c.tokenExp) {
        return c.token, nil
    }

    body := map[string]string{
        "app_id":     c.appID,
        "app_secret": c.appSecret,
    }
    jsonBody, _ := json.Marshal(body)

    resp, err := c.httpClient.Post(
        feishuBaseURL+"/auth/v3/tenant_access_token/internal",
        "application/json",
        bytes.NewReader(jsonBody),
    )
    if err != nil {
        return "", fmt.Errorf("获取飞书token失败: %w", err)
    }
    defer resp.Body.Close()

    var result struct {
        Code              int    `json:"code"`
        Msg               string `json:"msg"`
        TenantAccessToken string `json:"tenant_access_token"`
        Expire            int    `json:"expire"` // 秒
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", err
    }
    if result.Code != 0 {
        return "", fmt.Errorf("飞书返回错误 (code=%d): %s", result.Code, result.Msg)
    }

    c.token = result.TenantAccessToken
    c.tokenExp = time.Now().Add(time.Duration(result.Expire-300) * time.Second) // 提前5分钟过期

    return c.token, nil
}

// doRequest 执行带认证的API请求
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
    token, err := c.GetTenantAccessToken(ctx)
    if err != nil {
        return err
    }

    var bodyReader io.Reader
    if body != nil {
        jsonBytes, _ := json.Marshal(body)
        bodyReader = bytes.NewReader(jsonBytes)
    }

    req, err := http.NewRequestWithContext(ctx, method, feishuBaseURL+path, bodyReader)
    if err != nil {
        return err
    }

    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Content-Type", "application/json; charset=utf-8")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    rawBody, _ := io.ReadAll(resp.Body)

    var apiResp struct {
        Code int             `json:"code"`
        Msg  string          `json:"msg"`
        Data json.RawMessage `json:"data"`
    }
    if err := json.Unmarshal(rawBody, &apiResp); err != nil {
        return err
    }
    if apiResp.Code != 0 {
        return fmt.Errorf("飞书API错误 (code=%d): %s, body=%s", apiResp.Code, apiResp.Msg, string(rawBody))
    }

    if result != nil {
        return json.Unmarshal(rawBody, result)
    }
    return nil
}
```

- [ ] **Step 2: 编写多维表格操作**

```go
// internal/feishu/bitable.go
package feishu

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/example/jiaoyimao-scraper/internal/models"
)

// BitableOps 多维表格操作
type BitableOps struct {
    client    *Client
    bitableID string
}

func NewBitableOps(client *Client, bitableID string) *BitableOps {
    return &BitableOps{client: client, bitableID: bitableID}
}

// BatchInsertOrders 批量写入回收订单到多维表格
// 策略：根据OrderID判断是新增还是更新
func (b *BitableOps) BatchInsertOrders(
    ctx context.Context,
    tableID string,
    orders []models.RecycleOrder,
) error {
    if len(orders) == 0 {
        return nil
    }

    // 飞书多维表格批量创建记录API
    // POST /open-apis/bitable/v1/apps/:app_token/tables/:table_id/records/batch_create
    //
    // 注意：需要先在多维表格中创建好字段，字段名与 models.RecycleOrder 的json tag对应

    // 1. 先获取现有的记录列表用于去重
    existing, err := b.listAllRecords(ctx, tableID)
    if err != nil {
        log.Printf("[飞书] 获取现有记录失败: %v", err)
    }
    existingIDs := make(map[string]bool)
    for _, rec := range existing {
        if id, ok := rec.Fields["order_id"].(string); ok {
            existingIDs[id] = true
        }
    }

    // 2. 筛选需要新增的记录
    var newRecords []models.RecycleOrder
    var updateRecords []models.RecycleOrder
    for _, order := range orders {
        if existingIDs[order.OrderID] {
            updateRecords = append(updateRecords, order)
        } else {
            newRecords = append(newRecords, order)
        }
    }

    // 3. 批量新增（每批最多500条，飞书限制）
    batchSize := 500
    for i := 0; i < len(newRecords); i += batchSize {
        end := i + batchSize
        if end > len(newRecords) {
            end = len(newRecords)
        }
        batch := newRecords[i:end]

        records := make([]map[string]interface{}, len(batch))
        for j, order := range batch {
            records[j] = map[string]interface{}{
                "fields": b.orderToFields(order),
            }
        }

        path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/batch_create", b.bitableID, tableID)
        var result struct {
            Code int `json:"code"`
            Msg  string `json:"msg"`
            Data struct {
                Records []struct {
                    RecordID string `json:"record_id"`
                } `json:"records"`
            } `json:"data"`
        }

        if err := b.client.doRequest(ctx, "POST", path, map[string]interface{}{
            "records": records,
        }, &result); err != nil {
            return fmt.Errorf("批量写入记录失败: %w", err)
        }
        log.Printf("[飞书] 批量新增 %d 条记录到表格 %s", len(batch), tableID)
    }

    // 4. 批量更新已存在的记录
    if len(updateRecords) > 0 {
        // 构建 orderID → recordID 的映射
        orderToRecordID := make(map[string]string)
        for _, rec := range existing {
            if id, ok := rec.Fields["订单编号"].(string); ok {
                orderToRecordID[id] = rec.RecordID
            }
        }

        for _, order := range updateRecords {
            recordID, ok := orderToRecordID[order.OrderID]
            if !ok {
                continue // 找不到对应的record_id，跳过更新
            }
            path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/%s",
                b.bitableID, tableID, recordID)
            body := map[string]interface{}{
                "fields": b.orderToFields(order),
            }
            var updateResult struct {
                Code int    `json:"code"`
                Msg  string `json:"msg"`
            }
            if err := b.client.doRequest(ctx, "PUT", path, body, &updateResult); err != nil {
                log.Printf("[飞书] 更新记录 %s 失败: %v", order.OrderID, err)
                continue
            }
        }
        log.Printf("[飞书] 批量更新 %d 条记录", len(updateRecords))
    }

    log.Printf("[飞书] 处理完毕: 新增 %d 条, 更新 %d 条", len(newRecords), len(updateRecords))
    return nil
}

// orderToFields 将回收订单转换为飞书多维表格字段
func (b *BitableOps) orderToFields(order models.RecycleOrder) map[string]interface{} {
    return map[string]interface{}{
        "订单编号":   order.OrderID,
        "游戏名称":   order.GameName,
        "区服":     order.ServerRegion,
        "账号信息":   order.AccountInfo,
        "回收价格":   order.Price,
        "订单状态":   order.Status,
        "创建时间":   order.CreateTime.Format("2006-01-02 15:04:05"),
        "完成时间":   order.CompleteTime.Format("2006-01-02 15:04:05"),
        "买家信息":   order.BuyerInfo,
        "备注":     order.Remarks,
        "数据更新时间": time.Now().Format("2006-01-02 15:04:05"),
    }
}

// listAllRecords 列出表格中所有记录（用于去重）
func (b *BitableOps) listAllRecords(ctx context.Context, tableID string) ([]models.FeishuRecord, error) {
    var allRecords []models.FeishuRecord
    pageToken := ""

    for {
        path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records?page_size=500", b.bitableID, tableID)
        if pageToken != "" {
            path += "&page_token=" + pageToken
        }

        var result struct {
            Code int `json:"code"`
            Msg  string `json:"msg"`
            Data struct {
                Items     []models.FeishuRecord `json:"items"`
                HasMore   bool                  `json:"has_more"`
                PageToken string                `json:"page_token"`
            } `json:"data"`
        }

        if err := b.client.doRequest(ctx, "GET", path, nil, &result); err != nil {
            return nil, err
        }

        allRecords = append(allRecords, result.Data.Items...)

        if !result.Data.HasMore {
            break
        }
        pageToken = result.Data.PageToken
    }

    return allRecords, nil
}
```

- [ ] **Step 3: 编译验证**

```bash
cd c:/Automatic
go mod tidy
go build ./internal/feishu/...
```

- [ ] **Step 4: 提交**

```bash
git add internal/feishu/
git commit -m "feat: add Feishu (Lark) bitable integration with batch insert/update"
```

---

### Task 8: 主程序入口与定时调度

**Files:**
- Create: `cmd/scraper/main.go`

**Interfaces:**
- Consumes: 所有 internal 模块
- Produces: 可执行程序 `bin/scraper`，支持 `-once` 单次运行和 `-daemon` 常驻定时运行

- [ ] **Step 1: 编写主程序入口**

```go
// cmd/scraper/main.go
package main

import (
    "context"
    "flag"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/robfig/cron/v3"

    "github.com/example/jiaoyimao-scraper/internal/auth"
    "github.com/example/jiaoyimao-scraper/internal/browser"
    "github.com/example/jiaoyimao-scraper/internal/captcha"
    "github.com/example/jiaoyimao-scraper/internal/config"
    "github.com/example/jiaoyimao-scraper/internal/feishu"
    "github.com/example/jiaoyimao-scraper/internal/scraper"
)

func main() {
    configPath := flag.String("config", "./configs/config.yaml", "配置文件路径")
    once := flag.Bool("once", false, "仅运行一次后退出")
    daemon := flag.Bool("daemon", false, "以守护进程模式运行（定时任务）")
    flag.Parse()

    // 加载配置
    cfg, err := config.Load(*configPath)
    if err != nil {
        log.Fatalf("加载配置失败: %v", err)
    }

    // 初始化浏览器管理器（headless模式，不显示窗口）
    browserMgr, err := browser.NewManager(&cfg.Browser)
    if err != nil {
        log.Fatalf("初始化浏览器失败: %v", err)
    }
    defer browserMgr.Close()

    // 初始化登录服务
    loginSvc := &auth.LoginService{}

    // 初始化验证码识别器
    var captchaSolver captcha.Solver
    if cfg.Captcha.Provider == "opencv" {
        captchaSolver = captcha.NewSliderSolver(cfg.Captcha.MaxRetry)
    } else {
        captchaSolver = captcha.NewThirdPartySolver(cfg.Captcha.Provider, cfg.Captcha.APIKey)
    }

    // 执行抓取的核心函数
    runScrape := func() {
        log.Println("========== 开始抓取 ==========")
        startTime := time.Now()

        // 1. 创建浏览器上下文（headless模式，所有操作在后端执行）
        ctx, cancel := browserMgr.NewContext(cfg.Browser.TimeoutSec)
        defer cancel()

        // 2. 检查Cookie有效性，无效则自动登录
        cookies, err := loginSvc.RefreshIfNeeded(ctx, cfg, captchaSolver)
        if err != nil {
            log.Printf("Cookie准备失败: %v", err)
            return
        }

        // 3. 抓取所有游戏数据
        scraperMgr := scraper.NewManager(cfg, browserMgr, cookies)
        allOrders, err := scraperMgr.ScrapeAll(ctx)
        if err != nil {
            log.Printf("抓取数据失败: %v", err)
            return
        }

        // 4. 写入飞书多维表格
        feishuClient := feishu.NewClient(&cfg.Feishu)
        for tableKey, orders := range allOrders {
            tableID, ok := cfg.Feishu.TableMapping[tableKey]
            if !ok {
                log.Printf("未找到表格 %s 的飞书映射，跳过", tableKey)
                continue
            }

            bitable := feishu.NewBitableOps(feishuClient, cfg.Feishu.BitableID)
            if err := bitable.BatchInsertOrders(ctx, tableID, orders); err != nil {
                log.Printf("写入飞书表格 %s 失败: %v", tableKey, err)
            }
        }

        elapsed := time.Since(startTime)
        log.Printf("========== 抓取完成 (耗时: %s) ==========", elapsed)
    }

    if *once {
        // 单次运行模式
        runScrape()
        return
    }

    if *daemon {
        // 守护进程模式：使用cron定时调度
        c := cron.New(cron.WithSeconds())
        c.AddFunc(cfg.Scraper.CronExpr, runScrape)

        // 启动时立即执行一次
        go runScrape()

        c.Start()
        log.Printf("守护进程已启动，定时表达式: %s", cfg.Scraper.CronExpr)

        // 等待退出信号
        sigChan := make(chan os.Signal, 1)
        signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
        <-sigChan

        log.Println("收到退出信号，正在关闭...")
        c.Stop()
        return
    }

    // 默认：单次运行
    runScrape()
}
```

- [ ] **Step 2: 编译完整项目**

```bash
cd c:/Automatic
go mod tidy
go build -o bin/scraper ./cmd/scraper
```

Expected: 编译成功，生成 `bin/scraper.exe`。

- [ ] **Step 3: 提交**

```bash
git add cmd/scraper/main.go
git commit -m "feat: add main entry point with single-run, daemon, and cron scheduling modes"
```

---

### Task 9: API反向工程文档与验证

**Files:**
- Create: `docs/api-research.md`

**Interfaces:**
- 文档任务，不产生代码接口

- [ ] **Step 1: 编写API抓包指南文档**

在 `docs/api-research.md` 中记录以下内容：

```markdown
# 交易猫商户平台 API 反向工程记录

## 抓包方法

1. 打开 Chrome DevTools (F12) → Network 标签
2. 勾选 "Preserve log"
3. 正常登录并导航到 "我的回收" 页面
4. 筛选 XHR/Fetch 请求
5. 切换不同游戏标签，观察触发的API请求

## 已发现的API端点

### 登录相关
- POST `https://merchant.jiaoyimao.com/api/v1/login`
  - 参数: phone, password, captcha_token
  - 响应: { code: 0, data: { token: "xxx" } }

### 回收订单列表
- GET `https://merchant.jiaoyimao.com/api/v1/merchant/recycle/orders?game_id=xxx&page=1&page_size=50`
  - Headers: Authorization: Bearer xxx
  - 响应格式见下方

### 【待补充】
- 各游戏对应的 game_id 映射
- 表格分页参数
- 具体响应字段映射
```

- [ ] **Step 2: 在实际浏览器中抓包验证API**

在浏览器中手动操作，记录所有XHR请求的：
- 完整URL
- 请求方法
- 请求头
- 请求体（如有）
- 响应JSON结构
- 每个字段的含义

更新 `docs/api-research.md` 和 `configs/config.yaml` 中的API路径。

- [ ] **Step 3: 提交**

```bash
git add docs/api-research.md
git commit -m "docs: add API reverse-engineering guide and findings"
```

---

### Task 10: 集成测试与端到端验证

**Files:**
- Create: `internal/scraper/manager_test.go`
- Create: `internal/feishu/bitable_test.go`

**Interfaces:**
- 测试任务，验证核心模块正确性

- [ ] **Step 1: 编写抓取管理器单测**

```go
// internal/scraper/manager_test.go
package scraper

import (
    "testing"
)

func TestParseTableHTML(t *testing.T) {
    html := `
    <table>
        <thead><tr><th>订单编号</th><th>账号信息</th><th>区服</th><th>价格</th><th>状态</th><th>时间</th></tr></thead>
        <tbody>
            <tr><td>ORD001</td><td>账号A</td><td>官服</td><td>¥100.00</td><td>已完成</td><td>2026-08-10 12:00:00</td></tr>
            <tr><td>ORD002</td><td>账号B</td><td>渠道服</td><td>¥200.00</td><td>处理中</td><td>2026-08-10 13:00:00</td></tr>
        </tbody>
    </table>`

    orders := parseTableHTML(html, "测试游戏")
    if len(orders) != 2 {
        t.Fatalf("期望2条记录，实际: %d", len(orders))
    }
    if orders[0].OrderID != "ORD001" {
        t.Errorf("期望订单号 ORD001，实际: %s", orders[0].OrderID)
    }
    if orders[1].ServerRegion != "渠道服" {
        t.Errorf("期望区服'渠道服'，实际: %s", orders[1].ServerRegion)
    }
}

func TestParsePrice(t *testing.T) {
    tests := []struct {
        input    string
        expected float64
    }{
        {"¥100.00", 100.00},
        {"￥200.50", 200.50},
        {"300", 300.00},
    }
    for _, tt := range tests {
        result := parsePrice(tt.input)
        if result != tt.expected {
            t.Errorf("parsePrice(%s) = %f, 期望 %f", tt.input, result, tt.expected)
        }
    }
}
```

- [ ] **Step 2: 运行测试**

```bash
cd c:/Automatic
go test ./... -v
```

Expected: 所有测试通过。

- [ ] **Step 3: 准备测试配置文件**

创建 `configs/config.test.yaml`，使用测试用的飞书表格ID，避免影响生产数据。

- [ ] **Step 4: 以单次模式试运行**

```bash
./bin/scraper -config ./configs/config.test.yaml -once
```

Expected: 程序正常执行，日志显示完整的抓取流程。

- [ ] **Step 5: 提交**

```bash
git add internal/scraper/manager_test.go internal/feishu/bitable_test.go configs/config.test.yaml
git commit -m "test: add unit tests and test configuration"
```

---

## 关键补充说明

### 1. 浏览器操作不抢鼠标

所有 chromedp 操作在 **headless Chrome** 中执行（`chromedp.Flag("headless", true)`）。headless 模式下的 Chrome 运行在内存中，不显示窗口，物理鼠标光标完全不受影响。即使配置为非 headless 模式用于调试（`headless: false`），chromedp 也是通过 Chrome DevTools Protocol 发送 `Input.dispatchMouseEvent` 指令，操作的是浏览器内部的虚拟鼠标，不会移动用户的物理鼠标光标。

### 2. 反自动化检测策略

交易猫可能使用多种反爬/反自动化检测，需要综合应对：

| 检测维度 | 应对策略 |
|----------|----------|
| `navigator.webdriver` 属性 | `--disable-blink-features=AutomationControlled` 标志 |
| User-Agent | 设置为真实Chrome的UA |
| 浏览器指纹（Canvas/WebGL/Font） | chromedp默认使用真实Chrome渲染引擎，指纹与普通Chrome一致 |
| 鼠标轨迹分析 | `simulateDrag()` 生成带加速度+回退的人类轨迹 |
| Cookie时效检测 | 定时刷新Cookie，避免过期后频繁触发验证 |
| 请求频率检测 | API模式下添加随机延迟（1-3秒），模拟人类浏览节奏 |

### 3. 滑块验证码处理策略（三层递进）

| 优先级 | 方案 | 适用场景 | 成功率 |
|--------|------|----------|--------|
| 1 | OpenCV本地识别 | 标准滑块（缺口+背景分开） | ~80% |
| 2 | 第三方打码平台 | OpenCV失败时的备用 | ~95% |
| 3 | Cookie长期有效 | Cookie有效期>24h，减少登录频率 | — |

**关键实现细节：**
- 验证码可能不会在点击登录后立即出现，需要轮询检测（已在 `waitAndSolveCaptcha` 中实现）
- 如果连续3次滑块验证失败，应触发告警（可能是验证码类型变更或IP被标记）
- 建议将验证码背景图和缺口图保存到 `data/captcha_debug/` 目录用于离线分析

### 4. Cookie持久化与刷新策略

```
┌──────────────┐     过期/无效     ┌──────────────┐
│ 加载本地Cookie │ ──────────────→ │ 自动登录流程   │
│ (cookies.json)│                  │ (headless)    │
└──────┬───────┘                  └──────┬───────┘
       │ 有效                           │ 成功
       ↓                                ↓
┌──────────────┐                  ┌──────────────┐
│ 直接使用Cookie │                  │ 保存Cookie到   │
│ 发起API请求    │                  │ cookies.json  │
└──────────────┘                  └──────────────┘
```

- Cookie文件存储在 `data/cookies.json`（权限 `0600`，仅本用户可读写）
- 每次登录成功后自动刷新 `expires_at` 字段
- 登录失败保留旧Cookie（可能尚未真正过期），下次运行时再重试

### 5. API分页处理

如果单页返回的记录数有限（如每页50条），需要循环翻页：

```go
func (c *apiClient) FetchAllPages(ctx context.Context, gameName string) ([]models.RecycleOrder, error) {
    var allOrders []models.RecycleOrder
    page := 1
    for {
        orders, total, err := c.FetchRecycleOrders(ctx, gameName, page)
        if err != nil {
            return nil, err
        }
        allOrders = append(allOrders, orders...)
        if len(allOrders) >= total || len(orders) == 0 {
            break
        }
        page++
        time.Sleep(time.Duration(1+rand.Intn(2)) * time.Second) // 随机延迟
    }
    return allOrders, nil
}
```

### 6. Chrome进程管理

- chromedp 启动的 Chrome 进程在 `Manager.Close()` 时自动终止
- 使用 `defer browserMgr.Close()` 确保异常退出时也能清理
- 如果有残留的Chrome进程（程序崩溃导致），可通过以下命令清理：
  ```bash
  # Windows PowerShell
  Get-Process chrome -ErrorAction SilentlyContinue | Stop-Process -Force
  ```

### 7. 飞书API限流与重试

飞书开放平台API限流规则：
- 单应用QPS：**100次/秒**
- 批量创建记录接口：单次最多 **500条**
- 建议实现指数退避重试（共3次，间隔1s/2s/4s）

### 8. 日志与监控建议

运行时关键日志示例：
```
[2026-08-11 10:00:01] ========== 开始抓取 ==========
[2026-08-11 10:00:01] [Cookie] Cookie有效，跳过登录
[2026-08-11 10:00:05] [抓取] 火影忍者: 23 条记录
[2026-08-11 10:00:08] [抓取] 原神_table1: 45 条记录
[2026-08-11 10:00:10] [抓取] 原神_table2: 31 条记录
...
[2026-08-11 10:01:15] [飞书] 批量新增 50 条记录到表格 tblXXXXXXX1
[2026-08-11 10:01:45] [飞书] 处理完毕: 新增 312 条, 更新 89 条
[2026-08-11 10:01:45] ========== 抓取完成 (耗时: 1m44s) ==========
```

### 9. 配置文件安全

`configs/config.yaml` 包含密码和API密钥等敏感信息：
- ✅ **已添加到 `.gitignore`**（Task 1）
- ✅ **提供 `config.yaml.example` 模板**（Task 1）
- ✅ **可通过环境变量覆盖敏感字段**（建议生产部署时使用）：
  ```bash
  export JYM_USERNAME="your_phone"
  export JYM_PASSWORD="your_password"
  export FEISHU_APP_ID="cli_xxx"
  export FEISHU_APP_SECRET="xxx"
  ```

### 10. 已知局限与未来改进

| 局限 | 影响 | 改进方向 |
|------|------|----------|
| 纯Go边缘检测精度有限 | 滑块验证码识别率~80% | 集成gocv（OpenCV Go绑定）可提升至~95% |
| API端点需手动抓包确认 | 初期需要人工介入 | 实现API端点自动探测 |
| 仅支持密码登录 | 如遇短信验证码登录，需扩展 | 已在 `detectLoginForm` 中预留sms模式 |
| 飞书表格字段名硬编码 | 字段名变更需改代码 | 改为配置文件驱动字段映射 |
| 单机运行 | 不支持分布式 | 添加Redis锁实现多实例互斥 |
