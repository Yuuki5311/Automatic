# Board Stats Local Scrape Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将项目主路径改为抓取交易猫 6 游戏昨日看板统计（`recyclestats`），落盘本地 JSON；UI 可自动获取 Cookie；常驻进程每天 09:00 定时抓取。

**Architecture:** 在现有 Go 模块上替换抓取产出：MTOP `recyclestats` → `BoardStatsSnapshot` → `data/stats/YYYY-MM-DD.json`。`cmd/gui` 当前加载的是 `internal/web` HTTP 仪表盘（非 Wails App 绑定），因此登录/抓取 API 与 UI 改动以 `web` 包为准；`cmd/gui` 与 `cmd/scraper -daemon` 均挂载 cron `0 9 * * *`。飞书调用从主路径移除（包保留）。

**Tech Stack:** Go 1.26, chromedp, robfig/cron, net/http, encoding/json, slog

## Global Constraints

- 实现语言：Go only
- 接口：`mtop.com.jym.merchant.board.recyclestats`，`time=yesterday` only
- 落盘：`data/stats/YYYY-MM-DD.json`（昨日日期，覆盖写）；本阶段不写飞书
- Cookie 失效：先自动登录；失败则停止本轮并 UI 标红
- 默认 cron：`0 9 * * *`；默认 `scraper.mode: api`
- Spec：`docs/superpowers/specs/2026-08-13-board-stats-local-design.md`

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/models/models.go` | 新增 `BoardMetric` / `GameBoardStats` / `BoardStatsSnapshot` |
| `internal/statsstore/store.go` | 读写 `data/stats/*.json` |
| `internal/scraper/api.go` | 完整 MTOP 参数 + `FetchBoardStats` 解析 |
| `internal/scraper/manager.go` | `ScrapeBoardAll` 编排（按游戏，容错单游戏失败） |
| `internal/config/config.go` | `StatsDir` 默认、`cron`/`mode` 默认值 |
| `internal/status/status.go` | 看板结果字段（指标列表）；弱化飞书字段 |
| `cmd/scraper/main.go` | 抓取落盘、去掉飞书；daemon 不强制启动即抓 |
| `cmd/gui/main.go` | 挂 cron；可选把 scrape 能力注入 web |
| `cmd/cookie-tool/main.go` | 新增 `-login` |
| `internal/web/server.go` + `templates/index.html` | `/api/scrape`；看板 UI |
| `configs/config.yaml.example` | 同步默认配置 |

`cmd/gui/app.go` / `assets/*`：本阶段可不作为主路径；若时间允许，可改为与 web API 一致或标注废弃。优先保证 **实际打开的 HTTP 仪表盘** 可用。

---

### Task 1: Board models + local stats store

**Files:**
- Modify: `internal/models/models.go`
- Create: `internal/statsstore/store.go`
- Create: `internal/statsstore/store_test.go`
- Modify: `internal/models/models_test.go`（追加 board 模型 round-trip）
- Modify: `internal/config/config.go`（`StatsDir`）
- Modify: `configs/config.yaml.example`

**Interfaces:**
- Produces: `models.BoardMetric`, `models.GameBoardStats`, `models.BoardStatsSnapshot`
- Produces: `statsstore.Save(dir string, snap models.BoardStatsSnapshot) (string, error)` — 写入 `{dir}/{snap.Date}.json`，覆盖
- Produces: `statsstore.Load(dir, date string) (*models.BoardStatsSnapshot, error)`
- Produces: `config.ScraperConfig.StatsDir string`；`Load` 默认 `./data/stats`

- [ ] **Step 1: Write failing tests for models + store**

在 `internal/models/models_test.go` 追加：

```go
func TestBoardStatsSnapshotJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.Local)
	snap := BoardStatsSnapshot{
		Date:      "2026-08-12",
		ScrapedAt: now,
		Games: []GameBoardStats{{
			GameName:  "原神",
			GameID:    1009609,
			TimeKey:   "yesterday",
			FetchedAt: now,
			Metrics: []BoardMetric{
				{Title: "咨询量", Value: "186", Tips: "向您发起回收咨询的数量"},
				{Title: "回收成功金额", Value: "9650.00", Unit: "元"},
			},
		}},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var got BoardStatsSnapshot
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Date != snap.Date || len(got.Games) != 1 || got.Games[0].Metrics[0].Value != "186" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
```

在 `internal/statsstore/store_test.go`：

```go
package statsstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestSaveOverwriteSameDate(t *testing.T) {
	dir := t.TempDir()
	snap1 := models.BoardStatsSnapshot{
		Date: "2026-08-12", ScrapedAt: time.Now(),
		Games: []models.GameBoardStats{{GameName: "鸣潮", GameID: 2007615, TimeKey: "yesterday",
			Metrics: []models.BoardMetric{{Title: "咨询量", Value: "1"}}}},
	}
	snap2 := snap1
	snap2.Games[0].Metrics[0].Value = "229"
	if _, err := Save(dir, snap1); err != nil {
		t.Fatal(err)
	}
	path, err := Save(dir, snap2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	if got.Games[0].Metrics[0].Value != "229" {
		t.Fatalf("want overwrite 229, got %s", got.Games[0].Metrics[0].Value)
	}
	if filepath.Base(path) != "2026-08-12.json" {
		t.Fatalf("path=%s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
go test ./internal/models/ ./internal/statsstore/ -count=1
```

Expected: FAIL（类型或包不存在）

- [ ] **Step 3: Implement models + store + config default**

`internal/models/models.go` 追加（保留现有 `RecycleOrder`）：

```go
type BoardMetric struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Tips  string `json:"tips,omitempty"`
}

type GameBoardStats struct {
	GameName  string        `json:"game_name"`
	GameID    int           `json:"game_id"`
	TimeKey   string        `json:"time_key"`
	Metrics   []BoardMetric `json:"metrics"`
	FetchedAt time.Time     `json:"fetched_at"`
	RawJSON   string        `json:"raw_json,omitempty"`
	Error     string        `json:"error,omitempty"`
}

type BoardStatsSnapshot struct {
	Date      string           `json:"date"`
	ScrapedAt time.Time        `json:"scraped_at"`
	Games     []GameBoardStats `json:"games"`
}
```

`internal/statsstore/store.go`：

```go
package statsstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func Save(dir string, snap models.BoardStatsSnapshot) (string, error) {
	if snap.Date == "" {
		return "", fmt.Errorf("snapshot date empty")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, snap.Date+".json")
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func Load(dir, date string) (*models.BoardStatsSnapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, date+".json"))
	if err != nil {
		return nil, err
	}
	var snap models.BoardStatsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
```

`config.ScraperConfig` 增加 `StatsDir string \`yaml:"stats_dir"\``；在 `Load` 中若为空设为 `./data/stats`。  
`config.yaml.example`：`mode: "api"`，`cron_expr: "0 9 * * *"`，`stats_dir: "./data/stats"`。

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/models/ ./internal/statsstore/ ./internal/config/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/models/ internal/statsstore/ internal/config/config.go configs/config.yaml.example
git commit -m "feat: add board stats models and local stats store"
```

---

### Task 2: MTOP FetchBoardStats parse

**Files:**
- Modify: `internal/scraper/api.go`
- Create: `internal/scraper/api_board_test.go`（或扩展现有 test）
- Create: `internal/scraper/testdata/recyclestats_genshin.json`（可用 `data/recyclestats-all.json` 中原神片段整理）

**Interfaces:**
- Consumes: cookies / `mtopSign` / `gameNameToID`
- Produces: `(*apiClient) FetchBoardStats(ctx, gameName string) (models.GameBoardStats, error)`
- Produces: `ParseRecycleStatsJSON(gameName string, gameID int, body []byte) (models.GameBoardStats, error)`（纯函数便于测）

- [ ] **Step 1: Write failing parse test**

```go
func TestParseRecycleStatsJSON_GenshinEightMetrics(t *testing.T) {
	body := []byte(`{
	  "api":"mtop.com.jym.merchant.board.recyclestats",
	  "ret":["SUCCESS::调用成功"],
	  "data":{"result":[
	    {"title":"咨询量","staData":"186","properties":{"tips":"向您发起回收咨询的数量"}},
	    {"title":"发起报价量","staData":"50","properties":{"tips":"x"}},
	    {"title":"回收成功订单数","staData":"19","properties":{"tips":"x"}},
	    {"title":"回收成功金额","staData":"9650.00","staUnit":"元","properties":{"tips":"x"}},
	    {"title":"回收成功率","staData":"10.22","staUnit":"%","properties":{"tips":"x"}},
	    {"title":"回收满意度","staData":"100.00","staUnit":"%","properties":{"tips":"x"}},
	    {"title":"回收主页咨询量","staData":"0","properties":{"tips":"x"}},
	    {"title":"回收主页成功订单数","staData":"0","properties":{"tips":"x"}}
	  ]},
	  "v":"1.0"
	}`)
	got, err := ParseRecycleStatsJSON("原神", 1009609, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Metrics) != 8 {
		t.Fatalf("metrics=%d", len(got.Metrics))
	}
	if got.Metrics[0].Value != "186" || got.Metrics[3].Unit != "元" {
		t.Fatalf("%+v", got.Metrics)
	}
}

func TestParseRecycleStatsJSON_FailRet(t *testing.T) {
	_, err := ParseRecycleStatsJSON("火影忍者", 1003132, []byte(`{"ret":["FAIL_SYS_SESSION_EXPIRED::x"],"data":{}}`))
	if err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/scraper/ -run ParseRecycleStats -count=1
```

- [ ] **Step 3: Implement parse + FetchBoardStats**

要点：

- `buildRecycleStatsURL` 增加 `valueType=original`、`preventFallback=true`（对齐抓包）。
- `ParseRecycleStatsJSON`：检查 `ret` 前缀 `SUCCESS`；遍历 `data.result` → `BoardMetric{Title, Value:staData, Unit:staUnit, Tips}`；`RawJSON` 存 `data` 原始串；`TimeKey="yesterday"`。
- `FetchBoardStats`：按 gameId 请求，返回解析结果；失败返回 error（由 Manager 决定是否记入 `GameBoardStats.Error`）。
- 删除或停用 `FetchRecycleOrders` 在主路径的空返回；可保留函数但标注 deprecated，或改为调用 `FetchBoardStats` 仅供旧测——本任务优先让新 API 可用。
- `ProbeAPI` 可改为调用 `FetchBoardStats("火影忍者")` 忽略零值，或保留轻量 HEAD/GET 成功即可。

- [ ] **Step 4: Run tests — PASS**

```bash
go test ./internal/scraper/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/scraper/
git commit -m "feat: parse and fetch recyclestats board metrics"
```

---

### Task 3: Manager ScrapeBoardAll + wire scraper main (no Feishu)

**Files:**
- Modify: `internal/scraper/manager.go`
- Create: `internal/scraper/manager_board_test.go`
- Modify: `cmd/scraper/main.go`

**Interfaces:**
- Produces: `(*Manager) ScrapeBoardAll(ctx context.Context) (models.BoardStatsSnapshot, error)`
- `Date` = 本地时区昨日 `time.Now().AddDate(0,0,-1).Format("2006-01-02")`
- 单游戏失败：该条 `Error` 非空，继续；若所有游戏均 Error 则返回 error
- `cmd/scraper`：`statsstore.Save(cfg.Scraper.StatsDir, snap)`；**不再**调用 feishu；`RunInfo` 用成功游戏数或指标条数

- [ ] **Step 1: Failing manager test with fake apiClient**

若 `apiClient` 难 mock，可测纯辅助：

```go
func TestYesterdayDateLocal(t *testing.T) {
	d := YesterdayDate(time.Date(2026, 8, 13, 10, 0, 0, 0, time.Local))
	if d != "2026-08-12" {
		t.Fatal(d)
	}
}
```

并对 `ScrapeBoardAll` 用可注入的 fetcher 接口（推荐小重构）：

```go
type boardFetcher interface {
	FetchBoardStats(ctx context.Context, gameName string) (models.GameBoardStats, error)
}
```

Manager 内用 `boardFetcher`；测试注入 stub：一成功一失败。

- [ ] **Step 2: Implement ScrapeBoardAll + YesterdayDate**

```go
func YesterdayDate(now time.Time) string {
	return now.In(time.Local).AddDate(0, 0, -1).Format("2006-01-02")
}

func (m *Manager) ScrapeBoardAll(ctx context.Context) (models.BoardStatsSnapshot, error) {
	snap := models.BoardStatsSnapshot{Date: YesterdayDate(time.Now()), ScrapedAt: time.Now()}
	ok := 0
	for _, game := range m.cfg.Scraper.Games {
		gs, err := m.apiClient.FetchBoardStats(ctx, game.Name)
		if err != nil {
			gs = models.GameBoardStats{GameName: game.Name, TimeKey: "yesterday", Error: err.Error(), FetchedAt: time.Now()}
			if id, okID := gameNameToID[game.Name]; okID {
				gs.GameID = id
			}
		} else {
			ok++
		}
		snap.Games = append(snap.Games, gs)
		if m.report != nil {
			m.report(game.Name, len(gs.Metrics), err)
		}
	}
	if ok == 0 {
		return snap, fmt.Errorf("全部游戏看板抓取失败")
	}
	return snap, nil
}
```

- [ ] **Step 3: Rewrite `runScrape` in `cmd/scraper/main.go`**

- Cookie → `ScrapeBoardAll` → `statsstore.Save`
- 删除飞书循环与 `PhaseSyncing`（或保留 phase 名但改为 `PhaseIdle` 结束前写盘）
- `-daemon`：启动时 **不要** `go runScrape()`（符合 spec）；仅 cron 触发；可用 `-once` 手动
- `st.RunFinished`：`TotalOrders` 可复用为「成功游戏数」或后续 Task 4 改字段名

- [ ] **Step 4: Compile**

```bash
go build -o bin/scraper.exe ./cmd/scraper
go test ./internal/scraper/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/scraper/ cmd/scraper/main.go
git commit -m "feat: scrape board stats to local files; skip Feishu on main path"
```

---

### Task 4: Status + Web UI for board metrics + `/api/scrape`

**Files:**
- Modify: `internal/status/status.go`
- Modify: `internal/web/server.go`
- Modify: `internal/web/templates/index.html`
- Modify: `cmd/scraper/main.go` / 注入 scrape 回调到 web（若需要）

**Interfaces:**
- `GameResult` 增加 `Metrics []models.BoardMetric \`json:"metrics,omitempty"\``；`RecordCount` = `len(Metrics)`
- `RunInfo` 增加 `StatsDate string \`json:"stats_date,omitempty"\``；`TotalOrders` 语义改为成功游戏数或改名 `SuccessGames`（若改名需同步 JS）
- `POST /api/scrape` → 异步跑与 main 相同的看板抓取（需把 runScrape 抽成可注入函数，或 web.Server 持有 scrape 回调）

推荐：

```go
// web.Server
scrapeFn func() // 由 main 注入
mux.HandleFunc("/api/scrape", s.handleScrape)
```

- [ ] **Step 1: Extend status RecordGame to accept metrics**

```go
func (s *Store) RecordGameStats(gameName string, metrics []models.BoardMetric, err error) {
	// upsert Games entry: Success=err==nil, Metrics=metrics, RecordCount=len(metrics), Error=...
}
```

- [ ] **Step 2: Update HTML**

- 「各游戏数据」渲染 metrics 表格（title/value/unit）
- 去掉飞书同步列（或显示「本阶段未启用」）
- 操作区增加按钮「立即抓取」→ `POST /api/scrape`（web 模板目前可能没有，需补上）
- 文案：「下次定时：每天 09:00」
- 登录失败时 Cookie/登录区域红色提示（已有 login_phase，加强样式）

- [ ] **Step 3: Wire scraper main 注入 scrapeFn；gui 同样**

- [ ] **Step 4: Manual smoke（可选）**：`go run ./cmd/scraper -web -daemon` 后 curl status

- [ ] **Step 5: Commit**

```bash
git add internal/status/ internal/web/ cmd/scraper/main.go
git commit -m "feat: expose board scrape API and show metrics on dashboard"
```

---

### Task 5: GUI daemon cron 09:00 + cookie-tool -login

**Files:**
- Modify: `cmd/gui/main.go`
- Modify: `cmd/cookie-tool/main.go`
- Modify: `cmd/scraper/main.go`（确认 daemon 不启动即抓、cron 默认）

**Interfaces:**
- gui：`cron.AddFunc(cfg.Scraper.CronExpr, scrapeFn)`；`st.SetDaemonState(Running)`
- cookie-tool：`-login` flag → browser + `PerformLogin` + `SaveCookies`

- [ ] **Step 1: cookie-tool -login**

```go
doLogin := flag.Bool("login", false, "使用配置账号自动登录并保存 Cookie")
// ...
if *doLogin {
  browserMgr, err := browser.NewManager(&cfg.Browser)
  // captcha solver, PerformLogin, SaveCookies, print ok
}
```

- [ ] **Step 2: gui main 挂载 cron + scrape 注入 web**

扩展 `web.New` 签名增加 `scrapeFn func()`，或 `web.Server.SetScrapeFunc`。

gui 启动后：

```go
st.SetDaemonState(status.DaemonRunning)
c := cron.New(...)
c.AddFunc(cfg.Scraper.CronExpr, scrapeFn)
c.Start()
```

`scrapeFn` 与 scraper 共享逻辑：抽到 `internal/scraper` 或小包 `internal/run` 避免复制——**推荐**新建：

`internal/pipeline/board.go`：

```go
func RunBoardScrape(ctx context.Context, cfg *config.Config, st *status.Store, login *auth.LoginService, browserMgr *browser.Manager, solver captcha.Solver) error
```

`cmd/scraper`、`cmd/gui`、`web` 回调均调用此函数。

- [ ] **Step 3: Build**

```bash
go build -o bin/cookie-tool.exe ./cmd/cookie-tool
go build -o bin/gui.exe ./cmd/gui
go build -o bin/scraper.exe ./cmd/scraper
```

- [ ] **Step 4: Commit**

```bash
git add cmd/gui/main.go cmd/cookie-tool/main.go cmd/scraper/internal-or-pipeline...
git commit -m "feat: daily 09:00 cron in GUI and cookie-tool -login"
```

---

### Task 6: End-to-end verification

**Files:** none required unless fixes

- [ ] **Step 1: Unit tests all**

```bash
go test ./... -count=1
```

Expected: PASS（跳过需浏览器的 integration 若失败则用 `-short` 或已有 build tags）

- [ ] **Step 2: Real once scrape（需网络+有效 Cookie）**

```bash
go run ./cmd/cookie-tool -check -config ./configs/config.yaml
go run ./cmd/scraper -config ./configs/config.yaml -once
```

Expected: `data/stats/YYYY-MM-DD.json` 存在，6 游戏 metrics 齐全；日志无飞书错误。

- [ ] **Step 3: Fix any bugs found；commit if needed**

```bash
git commit -m "fix: board scrape verification follow-ups"
```

---

## Spec Coverage Check

| Spec 要求 | Task |
|-----------|------|
| 看板模型 + 本地 JSON | T1 |
| recyclestats 完整参数与解析 | T2 |
| 6 游戏编排、单游戏容错 | T3 |
| 不写飞书 | T3 |
| UI 指标展示 + 立即抓取 | T4 |
| 获取 Cookie 自动登录 | 已有 `/api/login`；T4 强化标红；T5 cookie-tool |
| 每天 09:00 常驻 | T5 |
| 登录失败停抓标红 | T3/T4 pipeline |
| Go CLI | T3/T5 |
| yesterday only | T2/T3 |

## Placeholder Scan

无 TBD；实现时代码块可按仓库现状微调导入路径，但接口名不得偏离本 plan。

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-13-board-stats-local.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — 每任务新开子代理，任务间审查  
2. **Inline Execution** — 本会话按 executing-plans 连续做完，设检查点  

Which approach?
