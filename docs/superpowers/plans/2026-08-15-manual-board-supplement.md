# 手工补充飞书看板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在「最近抓取」中标记失败并支持点账号手工填指标，写入飞书同日同行（整行覆盖含清空）。

**Architecture:** UI 弹窗 → `POST /api/board/manual` → 账户库补店铺/UID → `feishu.UpsertBoardManualRow`（EnsureFields + list + PUT/create，空指标显式清空）。

**Tech Stack:** Go, 现有 web 仪表盘, 飞书 bitable API

## Global Constraints

- 不做补抓/重抓；日期来自历史分组日且只读。
- 已存在「日期|账号|游戏」则整行覆盖（空字段清空）。
- 自动 `SyncBoardStats` 行为不变（仍不删除旧行）。

---

### Task 1: Feishu 手工整行 upsert

**Files:**
- Modify: `internal/feishu/board_sync.go`
- Modify: `internal/feishu/board_sync_test.go`

- [x] 新增 `UpsertBoardManualRow`…
- [x] 测试：create、update 清空、校验

### Task 2: Web API

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/accounts_api_test.go` 或新建 `manual_board_test.go`
- Modify: `internal/scraper` 导出游戏名列表（或 web 内复用 map）

- [x] `GET /api/board/games` 返回游戏名
- [x] `POST /api/board/manual` 校验 + 写飞书 + 可选追加 history `manual`
- [x] 测试

### Task 3: UI

**Files:**
- Modify: `internal/web/templates/index.html`
- Modify: `internal/web/server_test.go`（模板关键字）

- [x] 失败行高亮；账号可点
- [x] 弹窗选游戏填指标提交
- [x] 重建 dist exe

---
