# Web 状态仪表盘 + 结构化日志 设计文档

**日期:** 2026-08-12
**状态:** 待审批

## 背景

交易猫数据抓取项目当前仅通过 `log.Printf` 输出到控制台，缺乏运行时状态可视化和持久化日志。需要：
1. 一个 Web 仪表盘实时显示 Cookie 状态和抓取结果
2. 结构化日志（级别、时间戳、组件标签），同时输出到控制台和文件

## 架构概览

```
cmd/scraper/main.go
├── logger.Init(cfg.Log)        → slog 双输出（控制台 TextHandler + 文件 JSONHandler）
├── status.NewStore()            → 线程安全状态存储（sync.RWMutex）
├── web.New(store)               → HTTP 服务器（net/http）
│   ├── GET /                    → 仪表盘 HTML 页面（embed 模板）
│   ├── GET /api/status          → JSON 状态接口（5秒轮询）
│   └── GET /healthz             → 健康检查
└── runScrape()                  → 抓取流程，实时更新 Store
    ├── PhaseCookie → RefreshIfNeeded → SetCookie
    ├── PhaseScrape → ScrapeAll → RecordGame（每表回调）
    └── PhaseSync   → BatchInsertOrders → RunFinished
```

## 文件变更

### 新建文件

| 文件 | 说明 |
|------|------|
| `internal/status/status.go` | 线程安全 Store + Snapshot 数据模型 |
| `internal/logger/logger.go` | slog 初始化：双 handler、日志轮转、Cron 适配 |
| `internal/web/server.go` | HTTP 服务：路由注册、模板渲染、JSON API |
| `internal/web/templates/index.html` | 仪表盘页面（HTML + 内联 CSS + 原生 JS 轮询） |

### 修改文件

| 文件 | 变更 |
|------|------|
| `cmd/scraper/main.go` | 加 `-web` flag；初始化 logger/status/web；instrument runScrape |
| `internal/scraper/manager.go` | 加 `SetResultReporter` 回调；slog 迁移（7处） |
| `internal/auth/login.go` | slog 迁移（10处） |
| `internal/feishu/bitable.go` | slog 迁移（5处） |
| `internal/browser/browser.go` | slog 迁移（1处） |
| `internal/config/config.go` | 加 `WebConfig` + `LogConfig` 结构体及默认值 |
| `configs/config.yaml.example` | 加 web/log 配置段 |
| `cmd/scraper/main_test.go` | flag 列表加 `-web` |

## 数据模型

### Snapshot（API 响应 + 模板渲染）

```go
type Snapshot struct {
    DaemonState string       `json:"daemon_state"` // "running" | "stopped"
    Phase       string       `json:"phase"`         // "idle" | "cookie" | "scraping" | "syncing"
    Cookie      CookieInfo   `json:"cookie"`
    CurrentRun  *RunInfo     `json:"current_run,omitempty"`
    LastRun     *RunInfo     `json:"last_run,omitempty"`
    Games       []GameResult `json:"games"`
    ServerTime  time.Time    `json:"server_time"`
}

type CookieInfo struct {
    Present       bool      `json:"present"`
    Valid         bool      `json:"valid"`
    MaskedValue   string    `json:"masked_value"`   // "jym_ko…a8f3"
    ExpiresAt     time.Time `json:"expires_at"`
    RemainingSecs int64     `json:"remaining_seconds"`
    UpdatedAt     time.Time `json:"updated_at"`
}

type RunInfo struct {
    StartedAt   time.Time `json:"started_at"`
    EndedAt     time.Time `json:"ended_at"`
    DurationSec float64   `json:"duration_seconds"`
    Success     bool      `json:"success"`
    TotalOrders int       `json:"total_orders"`
    Error       string    `json:"error,omitempty"`
}

type GameResult struct {
    TableKey    string `json:"table_key"`
    GameName    string `json:"game_name"`
    Success     bool   `json:"success"`
    RecordCount int    `json:"record_count"`
    Error       string `json:"error,omitempty"`
}
```

### Store（线程安全）

```go
type Store struct {
    mu   sync.RWMutex
    snap Snapshot
}

func NewStore() *Store
func (s *Store) SetDaemonState(st string)
func (s *Store) SetPhase(p string)
func (s *Store) SetCookie(c *models.CookieData, valid bool)
func (s *Store) RunStarted()
func (s *Store) RunFinished(err error, totalOrders int)
func (s *Store) RecordGame(tableKey string, count int, err error)
func (s *Store) Snapshot() Snapshot
```

## 仪表盘 UI

### 布局

```
┌──────────────────────────────────────────────┐
│  交易猫数据抓取 · 状态面板          🟢 运行中  │
├──────────────────────────────────────────────┤
│  Cookie 状态                                 │
│  ┌──────────────────────────────────────┐   │
│  │ 令牌: jym_ko…a8f3    ✅ 有效         │   │
│  │ 过期: 2026-08-13 10:30 (剩余23小时)   │   │
│  └──────────────────────────────────────┘   │
│                                              │
│  最近抓取                                    │
│  ┌──────────────────────────────────────┐   │
│  │ 开始: 2026-08-12 14:00               │   │
│  │ 结束: 2026-08-12 14:02  耗时: 2m3s   │   │
│  │ 结果: ✅ 成功  共 312 条             │   │
│  └──────────────────────────────────────┘   │
│                                              │
│  各游戏结果                                  │
│  ┌──────────┬──────────┬────────────────┐   │
│  │ 游戏     │ 表格     │ 结果           │   │
│  ├──────────┼──────────┼────────────────┤   │
│  │ 火影忍者  │ -        │ ✅ 23条        │   │
│  │ 原神     │ 官服     │ ✅ 45条        │   │
│  │ 原神     │ 渠道服   │ ❌ 登录过期    │   │
│  └──────────┴──────────┴────────────────┘   │
└──────────────────────────────────────────────┘
```

### 技术细节

- 单文件 HTML，内联 CSS，无外部依赖
- Go `html/template` 服务端初始渲染（无 JS 也能看）
- 原生 `fetch` + `setInterval(5000)` 自动刷新
- 只用 `textContent` 更新 DOM，防止 XSS
- Cookie 值始终 masked（前6后4），原始值永不出现在页面或日志中
- 响应式：Cookie TTL < 1h 红色警告，1-24h 黄色，>24h 绿色

## 日志系统

### 配置

```yaml
log:
  level: "info"              # debug | info | warn | error
  file: "./data/scraper.log"
  max_size_mb: 10
  max_backups: 3
```

### 输出格式

**控制台** (`slog.NewTextHandler`):
```
2026-08-12T14:30:00.123+08:00 INFO scraper 抓取完成 table=火影忍者 count=23
```

**文件** (`slog.NewJSONHandler`):
```json
{"time":"2026-08-12T14:30:00.123+08:00","level":"INFO","component":"scraper","msg":"抓取完成","table":"火影忍者","count":23}
```

### 轮转策略

- **按天**: 日期变更时自动轮转为 `scraper.log.2026-08-11`
- **按大小**: 单文件超过 `max_size_mb` 时轮转为 `scraper.log.1`、`.2`...
- **Windows 兼容**: 轮转前先 `Close()` 再 `Rename()`（Windows 文件锁）
- 保留最近 `max_backups` 个备份

### 迁移映射（34处 log.Printf → slog）

| 原标签 | slog component | 级别 |
|--------|---------------|------|
| `[登录]` | `login` | Info（正常）/ Error（失败） |
| `[Cookie]` | `cookie` | Info（跳过）/ Warn（过期） |
| `[抓取]` | `scraper` | Info（完成）/ Warn（单表失败）/ Error（致命） |
| `[飞书]` | `feishu` | Info（写入）/ Warn（单条失败）/ Error（整批失败） |
| 无标签 | `main` | Info（进度）/ Error（致命） |

**原消息文本不变**，仅改为结构化键值对。

## 主程序集成

### 新增 Flag

```
-web    启用 Web 状态仪表盘（默认 false）
```

### 启动命令

```bash
# 单次 + 仪表盘
./bin/scraper -config ./configs/config.yaml -once -web

# 守护 + 仪表盘（推荐）
./bin/scraper -config ./configs/config.yaml -daemon -web
# 打开 http://127.0.0.1:8080
```

### 生命周期

- `-once -web`: 仪表盘在抓取期间可访问，完成后进程退出
- `-daemon -web`: 仪表盘常驻，SIGINT/SIGTERM 时优雅关闭（先关 HTTP，再停 cron）
- 不加 `-web`: 行为完全不变，向后兼容

## 验证计划

1. `go build ./...` — 编译通过
2. `go test ./... -count=1` — 所有测试通过
3. `./bin/scraper -config ./configs/config.test.yaml -once -web` — 启动后在浏览器确认仪表盘显示
4. 检查 `data/scraper.log` 是否有结构化日志输出
5. 模拟 Cookie 过期场景，确认仪表盘显示"无效"
6. `gofmt -l` / `go vet` — 无告警

## 风险

- **Windows 文件轮转**: rename 前必须先 Close — 已处理
- **Cookie 泄露**: masked 值永不出现在日志/JSON 中 — `SetCookie` 内部做 mask
- **并发安全**: Store 所有写操作走 `sync.RWMutex` — 已处理
- **零外部依赖**: `net/http`, `html/template`, `log/slog`, `embed` 全是标准库
