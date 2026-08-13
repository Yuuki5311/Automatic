# Rod Headless Browser Migration Plan

> **For agentic workers:** Implement task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Replace chromedp with go-rod/rod across the repo; default headless; no anti-detection.

**Architecture:** `internal/browser.Manager` owns `rod.Browser`; `NewContext` puts `*rod.Page` in context; auth/captcha/scraper use `browser.PageFromContext`.

**Tech Stack:** Go 1.26, go-rod/rod, go-rod/launcher

## Global Constraints

- No stealth / anti-detection / custom AutomationControlled / forged UA
- Default `headless: true`
- Remove chromedp dependencies from go.mod after migration

---

## File map

| File | Role |
|------|------|
| `internal/browser/browser.go` | Rod Manager + NewContext + PageFromContext |
| `internal/browser/page.go` | Navigate/Eval/Click/Input/Cookies helpers |
| `internal/browser/*_test.go` | Unit/integration tests for Rod API |
| `internal/auth/login.go` | Login via Page API |
| `internal/captcha/*.go` | Detect/Solve/drag via Page |
| `internal/scraper/browser.go` | DOM scrape via Page |
| `cmd/*` | Use new Manager; drop chromedp |
| `configs/*.yaml*` | `headless: true` |

---

### Task 1: browser package on Rod

- [ ] Add `github.com/go-rod/rod`
- [ ] Rewrite `Manager` / `NewContext` / `Close` / `findChrome`
- [ ] Add page helpers (Eval, Navigate, Sleep, SetCookies, GetCookies, Mouse drag)
- [ ] Update browser unit tests (no chromedp)
- [ ] `go test ./internal/browser/ -count=1`

### Task 2: auth + captcha

- [ ] Rewrite `login.go` / cookie extract for Rod
- [ ] Rewrite `baxia.go` + `slider.go` for Page Eval/Mouse
- [ ] Fix auth/captcha tests
- [ ] `go test ./internal/auth/ ./internal/captcha/ -count=1`

### Task 3: scraper + cmds + config

- [ ] Rewrite `scraper/browser.go`
- [ ] Update cookie-tool/gui/scraper/web/pipeline call sites if needed
- [ ] Fix or trim debug cmds
- [ ] Set `headless: true` in config example + local
- [ ] Remove chromedp from go.mod
- [ ] `go test ./...` (skip long integration if needed)
- [ ] Smoke: `go run ./cmd/cookie-tool -login` (expect possible captcha fail in headless; must not panic)
