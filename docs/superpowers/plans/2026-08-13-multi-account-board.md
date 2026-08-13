# Multi-Account Board Scrape Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the Web UI manage multiple Jiaoyimao accounts; each scrape run processes every enabled account serially; Feishu rows are distinguished by a new「账号」field.

**Architecture:** Persist accounts in `data/accounts.json` (`internal/accounts`). Pipeline loops enabled accounts: per-account cookie refresh → scrape → save under `stats/{date}/{account}.json` → Feishu upsert keyed by `日期|账号|游戏名称`. Status/UI expose per-account `ok|skipped|error` badges.

**Tech Stack:** Go, existing Rod browser, `internal/web` HTML panel, Feishu bitable APIs, JSON files under `data/` (gitignore).

## Global Constraints

- Distinguishing field name is exactly「账号」(not「店铺名称」); value = account `username` entered at add time.
- Cookie prefer, then auto-login with that account’s password; on failure mark `skipped`, continue others.
- Skipped accounts must be visible in UI (`last_status` + `last_error`).
- Serial browser use (one `browser.Manager`); `ErrScrapeRunning` unchanged.
- Feishu failure must not fail local save; account stays `ok` if scrape+save succeeded.
- API must not return plaintext passwords (`has_password` only).
- Migrate legacy `config.JYM` into accounts store when store is empty.
- No parallel Chromes, no Feishu historical backfill, no per-account game lists (YAGNI).

## File map

| Path | Responsibility |
|------|----------------|
| `internal/accounts/store.go` | Load/Save/Add/Delete/UpdateStatus/List/MigrateFromConfig |
| `internal/accounts/store_test.go` | CRUD + migration tests |
| `internal/models/models.go` | Add `Account` on `BoardStatsSnapshot` |
| `internal/statsstore/store.go` | Save/Load path `dir/date/safeAccount.json` |
| `internal/feishu/board_sync.go` | Field「账号」+ upsert key with account |
| `internal/config/config.go` | Optional helper `JYMForAccount` or keep overlay in pipeline |
| `internal/pipeline/board.go` | Multi-account `Run` loop |
| `internal/pipeline/board_test.go` | Skip-continue behavior |
| `internal/status/status.go` | `Accounts []AccountStatus` on snapshot |
| `internal/web/server.go` | `/api/accounts*` handlers |
| `internal/web/templates/index.html` | Accounts section UI |
| `cmd/scraper/main.go` / `cmd/gui` wire | Pass accounts path into BoardRun/Server |

---

### Task 1: `internal/accounts` store

**Files:**
- Create: `internal/accounts/store.go`
- Create: `internal/accounts/store_test.go`

**Interfaces:**
- Produces:
  - `type Account struct { ID, Username, Password, CookiePath string; Enabled bool; LastStatus, LastError string; LastRunAt time.Time }`
  - `type Store struct` with path
  - `func NewStore(path string) *Store`
  - `func (s *Store) Load() error` / `Save() error`
  - `func (s *Store) List() []Account` (copies)
  - `func (s *Store) Add(username, password string) (Account, error)` — rejects empty username; duplicate username error; sets `ID` (uuid or random hex), `CookiePath` = `filepath.Join(filepath.Dir(storePath), "cookies", safeUsername+".json")`, `Enabled=true`, `LastStatus="idle"`
  - `func (s *Store) Delete(id string) error` — remove entry; best-effort `os.Remove` cookie file
  - `func (s *Store) Get(id string) (Account, bool)`
  - `func (s *Store) UpdateStatus(id, status, errMsg string) error` — sets LastStatus, LastError, LastRunAt=now
  - `func (s *Store) SetPassword` not required first version
  - `func (s *Store) MigrateFromConfig(jym config.JYMConfig) (bool, error)` — if `len(accounts)==0` and `jym.Username!=""`, Add with existing CookiePath from config
  - `func SafeUsername(u string) string` — replace path-unsafe runes with `_`
  - Public view DTO later in web: not here

- [ ] **Step 1: Write failing tests**

```go
package accounts

import (
	"path/filepath"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

func TestAddListDelete(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "accounts.json"))
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	a, err := s.Add("13800000000", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if a.Username != "13800000000" || a.Password != "secret" || a.ID == "" {
		t.Fatalf("bad account: %+v", a)
	}
	if !filepath.IsAbs(a.CookiePath) && a.CookiePath == "" {
		t.Fatal("cookie path empty")
	}
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("expected empty")
	}
}

func TestAddDuplicate(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	_ = s.Load()
	if _, err := s.Add("u1", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("u1", "p2"); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestMigrateFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	s := NewStore(path)
	_ = s.Load()
	ok, err := s.MigrateFromConfig(config.JYMConfig{
		Username: "legacy", Password: "pw", CookiePath: filepath.Join(dir, "cookies.json"),
	})
	if err != nil || !ok {
		t.Fatalf("migrate: ok=%v err=%v", ok, err)
	}
	ok2, err := s.MigrateFromConfig(config.JYMConfig{Username: "other", Password: "x"})
	if err != nil || ok2 {
		t.Fatalf("second migrate should no-op: ok=%v err=%v", ok2, err)
	}
	if len(s.List()) != 1 || s.List()[0].Username != "legacy" {
		t.Fatalf("%+v", s.List())
	}
}

func TestUpdateStatus(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	a, _ := s.Add("u", "p")
	if err := s.UpdateStatus(a.ID, "skipped", "login failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(a.ID)
	if got.LastStatus != "skipped" || got.LastError != "login failed" || got.LastRunAt.IsZero() {
		t.Fatalf("%+v", got)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

Run: `go test ./internal/accounts/ -count=1`  
Expected: package not found or undefined `NewStore`

- [ ] **Step 3: Implement store**

```go
package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

type Account struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Password   string    `json:"password"`
	CookiePath string    `json:"cookie_path"`
	Enabled    bool      `json:"enabled"`
	LastStatus string    `json:"last_status"`
	LastError  string    `json:"last_error"`
	LastRunAt  time.Time `json:"last_run_at"`
}

type fileData struct {
	Accounts []Account `json:"accounts"`
}

type Store struct {
	path string
	mu   sync.Mutex
	data fileData
}

func NewStore(path string) *Store { return &Store{path: path} }

func SafeUsername(u string) string {
	var b strings.Builder
	for _, r := range u {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "account"
	}
	return s
}

func newID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.data = fileData{}
			return nil
		}
		return err
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	s.data = d
	return nil
}

func (s *Store) Save() error {
	// caller holds lock OR Save locked variant — implement locked save internally
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0600)
}

func (s *Store) List() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, len(s.data.Accounts))
	copy(out, s.data.Accounts)
	return out
}

func (s *Store) Get(id string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

func (s *Store) Add(username, password string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = strings.TrimSpace(username)
	if username == "" {
		return Account{}, fmt.Errorf("账号不能为空")
	}
	for _, a := range s.data.Accounts {
		if a.Username == username {
			return Account{}, fmt.Errorf("账号已存在: %s", username)
		}
	}
	dir := filepath.Join(filepath.Dir(s.path), "cookies")
	a := Account{
		ID:         newID(),
		Username:   username,
		Password:   password,
		CookiePath: filepath.Join(dir, SafeUsername(username)+".json"),
		Enabled:    true,
		LastStatus: "idle",
	}
	s.data.Accounts = append(s.data.Accounts, a)
	if err := s.saveLocked(); err != nil {
		return Account{}, err
	}
	return a, nil
}

func (s *Store) saveLocked() error { /* same as Save body using s.data */ }

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	var cookie string
	for i, a := range s.data.Accounts {
		if a.ID == id {
			idx, cookie = i, a.CookiePath
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("账户不存在")
	}
	s.data.Accounts = append(s.data.Accounts[:idx], s.data.Accounts[idx+1:]...)
	if err := s.saveLocked(); err != nil {
		return err
	}
	if cookie != "" {
		_ = os.Remove(cookie)
	}
	return nil
}

func (s *Store) UpdateStatus(id, status, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].LastStatus = status
			s.data.Accounts[i].LastError = errMsg
			s.data.Accounts[i].LastRunAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("账户不存在")
}

func (s *Store) Enabled() []Account {
	all := s.List()
	var out []Account
	for _, a := range all {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out
}

func (s *Store) MigrateFromConfig(jym config.JYMConfig) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Accounts) > 0 || strings.TrimSpace(jym.Username) == "" {
		return false, nil
	}
	cookie := jym.CookiePath
	if cookie == "" {
		cookie = filepath.Join(filepath.Dir(s.path), "cookies", SafeUsername(jym.Username)+".json")
	}
	a := Account{
		ID: newID(), Username: jym.Username, Password: jym.Password,
		CookiePath: cookie, Enabled: true, LastStatus: "idle",
	}
	s.data.Accounts = append(s.data.Accounts, a)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}
```

Complete `saveLocked` / unlock `Save` consistently (Load unlocks; Save can lock).

- [ ] **Step 4: Run tests — expect PASS**

Run: `go test ./internal/accounts/ -count=1`  
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/accounts/
git commit -m "feat: add multi-account JSON store"
```

---

### Task 2: Snapshot `Account` + statsstore per-account path

**Files:**
- Modify: `internal/models/models.go`
- Modify: `internal/statsstore/store.go`
- Modify: `internal/statsstore/store_test.go` (create if missing)

**Interfaces:**
- Produces: `BoardStatsSnapshot.Account string \`json:"account,omitempty"\``
- Produces: `Save` writes `filepath.Join(dir, snap.Date, SafeFile(snap.Account)+".json")` when Account set; if Account empty, keep legacy `dir/date.json` for backward compat OR always require account (prefer: if Account=="" use `"_default.json"` under date dir)

- [ ] **Step 1: Failing test for Save path**

```go
func TestSavePerAccount(t *testing.T) {
	dir := t.TempDir()
	snap := models.BoardStatsSnapshot{Date: "2026-08-12", Account: "13800000000", Games: nil}
	path, err := Save(dir, snap)
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join("2026-08-12", "13800000000.json")
	if !strings.HasSuffix(path, wantSuffix) {
		t.Fatalf("path=%s want suffix %s", path, wantSuffix)
	}
	loaded, err := Load(dir, "2026-08-12", "13800000000")
	if err != nil || loaded.Account != "13800000000" {
		t.Fatalf("%v %+v", err, loaded)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (Load signature / Account field)

- [ ] **Step 3: Implement**

```go
// models
type BoardStatsSnapshot struct {
	Date      string           `json:"date"`
	Account   string           `json:"account,omitempty"`
	ScrapedAt time.Time        `json:"scraped_at"`
	Games     []GameBoardStats `json:"games"`
}

// statsstore
func Save(dir string, snap models.BoardStatsSnapshot) (string, error) {
	if snap.Date == "" {
		return "", fmt.Errorf("snapshot date empty")
	}
	account := snap.Account
	if account == "" {
		account = "_default"
	}
	sub := filepath.Join(dir, snap.Date)
	if err := os.MkdirAll(sub, 0755); err != nil {
		return "", err
	}
	safe := accounts.SafeUsername(account) // or duplicate small helper to avoid cycle — prefer copy local safeFile in statsstore
	path := filepath.Join(sub, safe+".json")
	// marshal write 0600
	return path, nil
}

func Load(dir, date, account string) (*models.BoardStatsSnapshot, error) {
	if account == "" {
		account = "_default"
	}
	b, err := os.ReadFile(filepath.Join(dir, date, safeFile(account)+".json"))
	// ...
}
```

Avoid import cycle: put `safeFile` privately in `statsstore` (same rules as `accounts.SafeUsername`), do not import `accounts` from `statsstore`.

Update any callers of `Load(dir, date)` (grep) to new signature.

- [ ] **Step 4: `go test ./internal/statsstore/ ./internal/models/ -count=1`**

- [ ] **Step 5: Commit**

```bash
git add internal/models/models.go internal/statsstore/
git commit -m "feat: per-account board stats snapshot paths"
```

---

### Task 3: Feishu「账号」field + upsert key

**Files:**
- Modify: `internal/feishu/board_sync.go`
- Modify: `internal/feishu/board_sync_test.go` (or existing feishu tests)

**Interfaces:**
- Consumes: `snap.Account`
- Produces: field `boardFieldAccount = "账号"`; key `date+"|"+account+"|"+game`

- [ ] **Step 1: Extend tests**

Assert `boardGameToFields` includes `"账号": snap.Account`.  
Assert upsert key building uses account (table-driven unit test on exported helper or test via sync with httptest).

Minimal unit test without HTTP:

```go
func TestBoardGameToFieldsIncludesAccount(t *testing.T) {
	snap := models.BoardStatsSnapshot{Date: "2026-08-12", Account: "13800000000", ScrapedAt: time.Now()}
	g := models.GameBoardStats{GameName: "原神"}
	f := boardGameToFields(snap, g)
	if f[boardFieldAccount] != "13800000000" {
		t.Fatalf("%v", f)
	}
}

func TestBoardRecordKey(t *testing.T) {
	if got := boardRecordKey("2026-08-12", "138", "原神"); got != "2026-08-12|138|原神" {
		t.Fatal(got)
	}
}
```

- [ ] **Step 2: Run — FAIL** (missing const/helper)

- [ ] **Step 3: Implement**

```go
const boardFieldAccount = "账号"

// insert into boardFieldDefs after game or before game:
{boardFieldAccount, fieldTypeText},

func boardRecordKey(date, account, game string) string {
	return date + "|" + account + "|" + game
}

// In SyncBoardStats key map:
account := fieldAsString(rec.Fields[boardFieldAccount])
keyToID[boardRecordKey(dateKey, account, game)] = rec.RecordID

// When writing:
key := boardRecordKey(snap.Date, snap.Account, g.GameName)

// boardGameToFields:
fields[boardFieldAccount] = snap.Account
```

Update comment on `SyncBoardStats` to「日期+账号+游戏」.

- [ ] **Step 4: `go test ./internal/feishu/ -count=1`**

- [ ] **Step 5: Commit**

```bash
git add internal/feishu/
git commit -m "feat: feishu board upsert by date+account+game"
```

---

### Task 4: Config overlay for per-account login

**Files:**
- Modify: `internal/config/config.go` (add helper)
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `func (c *Config) WithJYMAccount(username, password, cookiePath string) *Config` — shallow copy Config, override `JYM.Username/Password/CookiePath`, return new pointer so `PerformLogin` / `RefreshIfNeeded` unchanged.

```go
func (c *Config) WithJYMAccount(username, password, cookiePath string) *Config {
	if c == nil {
		return &Config{JYM: JYMConfig{Username: username, Password: password, CookiePath: cookiePath}}
	}
	cp := *c
	cp.JYM = c.JYM
	cp.JYM.Username = username
	cp.JYM.Password = password
	cp.JYM.CookiePath = cookiePath
	return &cp
}
```

- [ ] **Step 1: Test copy isolation** (mutating overlay must not change original JYM.Username)

- [ ] **Step 2–4: Implement + `go test ./internal/config/ -count=1` + commit**

```bash
git commit -m "feat: config overlay for per-account JYM credentials"
```

---

### Task 5: Multi-account `BoardRun`

**Files:**
- Modify: `internal/pipeline/board.go`
- Modify: `internal/pipeline/board_test.go`

**Interfaces:**
- Consumes: `*accounts.Store`, existing login/scrape deps
- Change `NewBoardRun` to accept `acct *accounts.Store` (or set field after)
- Replace single PrepCookies path with loop over `acct.Enabled()`
- Per account: `cfg := r.Cfg.WithJYMAccount(...)`; `RefreshIfNeeded(ctx, cfg, solver)`; on err → `UpdateStatus(id,"skipped",err.Error())` + continue; on success scrape with cookies, set `snap.Account = username`, Save, SyncFeishu, `UpdateStatus(id,"ok","")`
- If no accounts: return/finish with error message in store (do not panic)
- Keep `TryLock` / `ErrScrapeRunning`
- `PrepCookies`/`Scrape` hooks: either keep for tests by introducing `RunAccount` injectable funcs:

```go
type BoardRun struct {
	// ...
	Accounts *accounts.Store
	LoginSvc *auth.LoginService
	Solver   captcha.Solver
	Browser  *browser.Manager // only if needed
	// Test hooks:
	PrepAccountCookies func(ctx context.Context, acct accounts.Account) (*models.CookieData, error)
	ScrapeAccount      func(ctx context.Context, acct accounts.Account, cookies *models.CookieData) (models.BoardStatsSnapshot, error)
}
```

Default implementations use `WithJYMAccount` + existing manager scrape.

- [ ] **Step 1: Failing test — skip continue**

```go
func TestBoardRun_SkipsFailedAccountContinues(t *testing.T) {
	dir := t.TempDir()
	as := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = as.Load()
	a1, _ := as.Add("bad", "x")
	a2, _ := as.Add("good", "y")
	st := status.NewStore()
	var scraped []string
	r := &BoardRun{
		Cfg: &config.Config{Scraper: config.ScraperConfig{StatsDir: filepath.Join(dir, "stats")}},
		Store: st,
		Accounts: as,
		NewContext: func(int) (context.Context, context.CancelFunc) {
			return context.Background(), func() {}
		},
		PrepAccountCookies: func(ctx context.Context, acct accounts.Account) (*models.CookieData, error) {
			if acct.Username == "bad" {
				return nil, errors.New("login failed")
			}
			return &models.CookieData{
				Cookies: []models.CookieEntry{{Name: "token", Value: "1"}},
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil
		},
		ScrapeAccount: func(ctx context.Context, acct accounts.Account, c *models.CookieData) (models.BoardStatsSnapshot, error) {
			scraped = append(scraped, acct.Username)
			return models.BoardStatsSnapshot{
				Date: "2026-08-12", Account: acct.Username,
				Games: []models.GameBoardStats{{GameName: "原神"}},
			}, nil
		},
		Save: statsstore.Save,
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if len(scraped) != 1 || scraped[0] != "good" {
		t.Fatalf("scraped=%v", scraped)
	}
	bad, _ := as.Get(a1.ID)
	good, _ := as.Get(a2.ID)
	if bad.LastStatus != "skipped" || good.LastStatus != "ok" {
		t.Fatalf("bad=%+v good=%+v", bad, good)
	}
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Rewrite `Run` loop** (keep lock; loop Enabled; wire defaults in `NewBoardRun`)

Pseudo:

```go
func (r *BoardRun) Run() error {
	if !r.mu.TryLock() { return ErrScrapeRunning }
	defer r.mu.Unlock()
	r.Store.RunStarted()
	accts := []accounts.Account{}
	if r.Accounts != nil {
		accts = r.Accounts.Enabled()
	}
	if len(accts) == 0 {
		err := errors.New("没有可用账户，请先在 UI 添加账号")
		r.Store.RunFinished(err, 0)
		return nil
	}
	okGames := 0
	for _, acct := range accts {
		r.Store.SetPhase(status.PhaseCookie)
		ctx, cancel := r.NewContext(timeout)
		cookies, err := r.prep(ctx, acct)
		if err != nil {
			_ = r.Accounts.UpdateStatus(acct.ID, "skipped", err.Error())
			r.Store.UpsertAccountStatus(...) // task 6
			cancel()
			continue
		}
		r.Store.SetPhase(status.PhaseScraping)
		snap, err := r.scrape(ctx, acct, cookies)
		snap.Account = acct.Username
		cancel()
		if err != nil {
			_ = r.Accounts.UpdateStatus(acct.ID, "skipped", err.Error())
			continue
		}
		path, saveErr := r.Save(statsDir, snap)
		if saveErr != nil {
			_ = r.Accounts.UpdateStatus(acct.ID, "skipped", saveErr.Error())
			continue
		}
		if r.SyncFeishu != nil {
			_, _, syncErr := r.SyncFeishu(ctxBg, snap)
			if syncErr != nil { slog.Error(...) }
		}
		_ = r.Accounts.UpdateStatus(acct.ID, "ok", "")
		for _, g := range snap.Games { if g.Error == "" { okGames++ } }
		_ = path
	}
	r.Store.RunFinished(nil, okGames)
	return nil
}
```

Use fresh context per account (important after cancel).

- [ ] **Step 4: `go test ./internal/pipeline/ -count=1`**

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: scrape all enabled accounts with skip-continue"
```

---

### Task 6: Status snapshot accounts for UI polling

**Files:**
- Modify: `internal/status/status.go`
- Modify: `internal/status/status_test.go`

**Interfaces:**

```go
type AccountStatus struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Enabled      bool      `json:"enabled"`
	LastStatus   string    `json:"last_status"`
	LastError    string    `json:"last_error,omitempty"`
	LastRunAt    time.Time `json:"last_run_at,omitempty"`
	CookieValid  bool      `json:"cookie_valid"`
	HasPassword  bool      `json:"has_password"`
}

// Snapshot add:
Accounts []AccountStatus `json:"accounts,omitempty"`

func (s *Store) SetAccounts(list []AccountStatus)
```

Pipeline/web refresh from `accounts.Store` + `auth.IsCookieValid(LoadCookies(path))` when building status.

- [ ] **Step 1–4: Test SetAccounts appears in Snapshot(); implement; test; commit**

```bash
git commit -m "feat: expose per-account status on dashboard snapshot"
```

---

### Task 7: Web API for accounts

**Files:**
- Modify: `internal/web/server.go`
- Create: `internal/web/accounts_api_test.go` (httptest)

**Interfaces:**
- Server holds `Accounts *accounts.Store`, `LoginSvc`, `Browser`, `Solver` (already has most)
- Routes:
  - `GET /api/accounts`
  - `POST /api/accounts` body `{"username":"...","password":"..."}`
  - `DELETE /api/accounts/{id}`
  - `POST /api/accounts/{id}/cookies` raw body EditThisCookie JSON → `auth.ImportFromJSON(acct.CookiePath, body)`
  - `POST /api/accounts/{id}/login` async like existing login but `WithJYMAccount`
- After mutations call `s.refreshAccountStatus()` writing into `store.SetAccounts`
- Keep `POST /api/login` as: if exactly one account, login that one; else 400 asking to use per-account login

Response list item:

```json
{"id":"...","username":"...","enabled":true,"last_status":"skipped","last_error":"...","cookie_valid":false,"has_password":true}
```

- [ ] **Step 1: httptest tests for Add + List hides password**

- [ ] **Step 2–4: Implement handlers + tests + commit**

```bash
git commit -m "feat: web API for multi-account CRUD and cookie import"
```

---

### Task 8: Dashboard UI accounts section

**Files:**
- Modify: `internal/web/templates/index.html`

**UI:**
- New `.section`「账户管理」above or replacing single-cookie-only controls
- Table columns: 账号 | Cookie | 状态徽章 | 错误 | 操作（导入 Cookie / 登录 / 删除）
- Form: username + password +「添加账户」
- Import: per-row textarea or prompt paste → `POST /api/accounts/{id}/cookies`
- Poll `/api/status` already; render `data.accounts` badges: `ok` green, `skipped`/`error` red/yellow
- 「立即抓取」unchanged → `/api/scrape`

- [ ] **Step 1: Implement HTML/JS** (no separate frontend build)

- [ ] **Step 2: Manual smoke** — `go run ./cmd/scraper -config configs/config.yaml -web` open UI, add account (optional)

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: dashboard UI for multi-account management"
```

---

### Task 9: Wire main entrypoints + migrate on startup

**Files:**
- Modify: `cmd/scraper/main.go`
- Modify: `cmd/gui/main.go` / `daemon.go` as needed
- Modify: `internal/web/server.go` `New(...)` signature

**Steps:**
- Accounts path: `./data/accounts.json` (or `filepath.Join(filepath.Dir(cfg.JYM.CookiePath), "accounts.json")`)
- `acctStore := accounts.NewStore(path); _ = acctStore.Load(); _, _ = acctStore.MigrateFromConfig(cfg.JYM)`
- Pass into `pipeline.NewBoardRun(..., acctStore)` and `web.New(..., acctStore)`
- On status poll, rebuild account statuses from store

- [ ] **Step 1: Compile** `go build ./cmd/scraper/ ./cmd/gui/`

- [ ] **Step 2: `go test ./internal/accounts/ ./internal/pipeline/ ./internal/feishu/ ./internal/web/ ./internal/statsstore/ ./internal/status/ ./internal/config/ -count=1`**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: wire multi-account store into scraper and GUI"
```

---

## Spec coverage self-check

| Spec requirement | Task |
|------------------|------|
| UI add account+password | 7, 8 |
| Per-account cookie import + login | 7, 8 |
| Scrape all enabled serially | 5, 9 |
| Cookie then auto-login | 4, 5 |
| Skip failed + UI mark | 5, 6, 8 |
| Feishu「账号」+ new upsert key | 3 |
| Migrate legacy config | 1, 9 |
| Per-account stats files | 2 |
| No password in API | 7 |
| Feishu fail ≠ local fail | 5 |

## Placeholder scan

No TBD steps; SafeUsername duplicated carefully to avoid cycles; `Load` callers must be grepped in Task 2.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-13-multi-account-board.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks  
2. **Inline Execution** — execute tasks in this session with executing-plans checkpoints  

Which approach?
