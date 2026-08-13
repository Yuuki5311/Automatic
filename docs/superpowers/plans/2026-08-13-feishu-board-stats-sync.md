# Feishu Board Stats Sync Implementation Plan

> **For agentic workers:** Implement task-by-task.

**Goal:** After board scrape save, upsert one row per game/day into Feishu bitable; auto-create fields if missing.

**Architecture:** Extend `internal/feishu` with EnsureBoardFields + SyncBoardStats; call from `pipeline.BoardRun` when Feishu configured.

**Tech Stack:** Go, existing feishu.Client, bitable v1 APIs

## Global Constraints

- Upsert key: 日期 + 游戏名称
- app_token / table_id from config
- Feishu failure must not fail local save
- Field names exactly as design doc

---

### Task 1: feishu board sync API

- [ ] Add board field constants + EnsureBoardFields (list fields, create missing)
- [ ] SyncBoardStats: list records, upsert by date+game
- [ ] Map metrics titles → number fields; date as ms timestamp
- [ ] Unit tests with httptest
- [ ] `go test ./internal/feishu/ -count=1`

### Task 2: config + pipeline wire-up

- [ ] Add `board_table_id` to FeishuConfig; set tokens in example + local config placeholders
- [ ] BoardRun: after Save, SyncBoardStats if client configured
- [ ] Tests for pipeline optional sync
- [ ] `go test ./internal/pipeline/ ./internal/config/ -count=1`
