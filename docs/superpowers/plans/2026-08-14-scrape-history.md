# Scrape History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist per-account scrape results for 30 days and show them in UI「最近抓取」grouped by calendar day.

**Architecture:** New `internal/scrapehistory` JSON store (`data/scrape_history.json`). Pipeline appends one entry after each account finishes. `status.Snapshot.ScrapeHistory` exposes entries for `/api/status` and the dashboard template, which groups by local date.

**Tech Stack:** Go, encoding/json, existing status/web/pipeline packages.

## Global Constraints

- Per-account one row per run; retain 30 days; fields: time, account, status, error summary.
- No SQLite; do not change Feishu/stats JSON paths.
- Status values: `ok` / `skipped` (error text in `Error`).

## File map

| File | Role |
|------|------|
| `internal/scrapehistory/store.go` | Load/Append/List/prune |
| `internal/scrapehistory/store_test.go` | Store tests |
| `internal/status/status.go` | `HistoryEntry` + Snapshot field + SetScrapeHistory |
| `internal/pipeline/board.go` | Inject history; append after account |
| `internal/pipeline/board_test.go` | Assert history rows |
| `cmd/gui/main.go`, `cmd/scraper/main.go` | Construct store, sync to status |
| `internal/web/templates/index.html` | Day-grouped list |
| `internal/web/server_test.go` | Dashboard contains history markers |

---

### Task 1: scrapehistory store

**Files:**
- Create: `internal/scrapehistory/store.go`
- Create: `internal/scrapehistory/store_test.go`

**Produces:** `NewStore(path)`, `Load()`, `Append(account, status, errMsg)`, `List() []Entry`, `PathFromDataDir(dir)`, const `Retention = 30*24*time.Hour`

- [ ] **Step 1:** Failing tests for Append+List, prune >30d, corrupt JSON → empty
- [ ] **Step 2:** Implement store (mutex, JSON `{entries:[]}`, truncate error to 200 runes, newest-first List)
- [ ] **Step 3:** `go test ./internal/scrapehistory/ -count=1`
- [ ] **Step 4:** Commit `feat: add scrape history JSON store`

### Task 2: status snapshot field

**Files:**
- Modify: `internal/status/status.go`
- Modify: `internal/status/status_test.go`

**Produces:** `type HistoryEntry` (same JSON tags as store entry or map from store), `Snapshot.ScrapeHistory`, `SetScrapeHistory([]HistoryEntry)`

- [ ] **Step 1:** Test SetScrapeHistory appears in Snapshot
- [ ] **Step 2:** Implement + deep-copy in Snapshot()
- [ ] **Step 3:** Commit `feat: expose scrape history on status snapshot`

### Task 3: pipeline append + wire cmds

**Files:**
- Modify: `internal/pipeline/board.go`
- Modify: `internal/pipeline/board_test.go`
- Modify: `cmd/gui/main.go`, `cmd/scraper/main.go`

**Produces:** `BoardRun.History interface{ Append(...); List() }`, after each account UpdateStatus call Append and `Store.SetScrapeHistory`

- [ ] **Step 1:** Extend skip/success board tests to assert 2 history entries
- [ ] **Step 2:** Implement hooks + NewBoardRun optional history param OR set field after NewBoardRun
- [ ] **Step 3:** In gui/scraper: `hist := scrapehistory.NewStore(...); Load(); board.History = hist; st.SetScrapeHistory(mapEntries(hist.List()))`
- [ ] **Step 4:** Commit `feat: record per-account scrape history in pipeline`

### Task 4: UI 最近抓取

**Files:**
- Modify: `internal/web/templates/index.html`
- Modify: `internal/web/server_test.go` (TestDashboardAccountsSection or new test)

**Produces:** Replace LastRun grid with CurrentRun banner + day-grouped history; JS poll refreshes history when `scrape_history` changes

- [ ] **Step 1:** Template + client render by date (今天/昨天/YYYY-MM-DD)
- [ ] **Step 2:** Test dashboard contains `scrape_history` / `最近抓取` structure without requiring old LastRun primary UI
- [ ] **Step 3:** Commit `feat: show day-grouped scrape history in dashboard`
- [ ] **Step 4:** Rebuild `dist/jiaoyimao-scraper` exes

---

## Spec coverage

- 30d prune → Task 1
- Per-account write → Task 3
- Status/API → Task 2–3
- UI by day/account → Task 4
- Restart persist → Task 1+3 path under data/
