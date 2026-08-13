# 多账号看板抓取与飞书「账号」区分设计

**日期:** 2026-08-13  
**状态:** 已批准  
**语言:** Go  

## 背景

当前系统为单账号：`config.yaml` 中一组 `jiaoyimao` 凭据 + 单一 `cookie_path`；Web/Wails 面板仅支持对该账号登录/导入 Cookie；飞书看板 upsert 键为「日期|游戏名称」，无法区分多个商家账号。

业务需要：在可视化 UI 手动添加多个账号（及密码），自动/手动抓取覆盖全部已启用账号；写入多维表格时用字段「账号」区分。

## 目标

1. UI 支持账户 CRUD：添加时填写账号+密码；可按账户导入 Cookie、触发该账户自动登录。
2. 定时与「立即抓取」顺序遍历所有 `enabled` 账户；Cookie 优先，失效则用该账户密码自动登录并落盘 Cookie。
3. 单账户失败（Cookie 无效且登录失败等）→ 标记为跳过，写入原因，**继续**其余账户；UI 展示跳过/错误徽章与摘要。
4. 飞书新增文本列「账号」；upsert 键改为 `日期|账号|游戏名称`。
5. 兼容：账户库为空时，将旧 `jiaoyimao.username/password/cookie_path` 迁入一条。

## 非目标（首版）

- 并行多浏览器 / 每账户独立 Chrome 进程。
- 飞书历史行按旧键回填「账号」。
- 短信登录；按账户覆盖 `scraper.games`（游戏列表仍全局）。
- 反检测或滑块轨迹大改。
- API 返回明文密码。

## 方案选择

采用**独立账户库** `data/accounts.json`（非把多账号列表主要写入 `config.yaml`），运行时状态与凭据同库便于 UI 轮询；`data/` 已 gitignore。

## 数据模型

### `data/accounts.json`

```json
{
  "accounts": [
    {
      "id": "uuid",
      "username": "1760...",
      "password": "...",
      "cookie_path": "./data/cookies/<safe-username>.json",
      "enabled": true,
      "last_status": "ok | skipped | error",
      "last_error": "",
      "last_run_at": "2026-08-13T17:00:00+08:00"
    }
  ]
}
```

| 字段 | 说明 |
|------|------|
| `username` | 添加时填写；飞书「账号」列取值 |
| `password` | 本地存储；列表 API 不返回明文，可返回 `has_password` |
| `cookie_path` | 每账户独立 Cookie 文件，避免覆盖 |
| `last_status` | `ok` / `skipped` / `error`（及未跑过的空或 `idle`） |
| `last_error` | 跳过/失败原因，供 UI 展示 |

文件权限建议 `0600`。新增包建议：`internal/accounts`（Load/Save/CRUD/MigrateFromConfig）。

### 看板快照

`BoardStatsSnapshot`（或每条 `GameBoardStats`）增加 `Account string`，同步飞书与落盘均携带。

### 本地统计落盘

路径改为按账户隔离，例如：`data/stats/{date}/{safeAccount}.json`，避免多账户互相覆盖。

## UI 与 API

在现有 Web 面板（`internal/web`，Wails 加载同一服务）增加「账户管理」区块：

- 列表：账号、Cookie 有效性摘要、最近结果徽章（正常 / 跳过 / 错误）、错误摘要。
- 操作：添加、删除、按账户导入 Cookie、按账户登录刷新。
- 「立即抓取」仍全局一次，内部跑全部 enabled 账户。

| 方法 | 路径 | 作用 |
|------|------|------|
| GET | `/api/accounts` | 列表（无明文密码） |
| POST | `/api/accounts` | 添加 `{username, password}` |
| DELETE | `/api/accounts/{id}` | 删除账户（可删除对应 cookie 文件） |
| POST | `/api/accounts/{id}/cookies` | 导入该账户 Cookie（EditThisCookie JSON） |
| POST | `/api/accounts/{id}/login` | 仅该账户自动登录并保存 Cookie |
| GET | `/api/status` | 扩展 `accounts[]`，供轮询刷新徽章 |

旧无参 `POST /api/login`：首版可保留为兼容（对默认/唯一账户）或标记废弃；不强制「全体一键登录」。

## 抓取流水线

改造 `pipeline.BoardRun`（或上层编排）为多账户循环，**串行**共用现有 `browser.Manager`：

1. 加载账户库 `enabled` 列表。
2. 对每个账户：  
   - `RefreshIfNeeded` 语义改为绑定该账户的 `username/password/cookie_path`（勿再用全局唯一 `cfg.JYM` 写死路径，或临时构造 per-account 配置视图）。  
   - 失败 → 更新 `last_status=skipped`、`last_error`，continue。  
   - 成功 → scrape → save（带 Account）→ SyncFeishu → `last_status=ok`。
3. 整轮结束更新 status 汇总（成功数 / 跳过数）。
4. 并发第二次 `Run` 仍返回 `ErrScrapeRunning`。

失败策略：**跳过并继续**（已确认）；跳过必须在 UI 可见。

## 飞书同步

- `EnsureBoardFields` 增加「账号」文本字段。
- `boardGameToFields` 写入「账号」。
- upsert 查找键：`日期|账号|游戏名称`（替换原 `日期|游戏名称`）。
- 无「账号」的旧记录不自动迁移；新写入走新键。

## 错误处理

| 场景 | 行为 |
|------|------|
| 单账户 Cookie+登录失败 | 跳过该账户，UI 标记，继续 |
| 单账户 scrape 部分游戏失败 | 账户 `last_status=ok`；游戏级错误写入快照/飞书「错误信息」；面板账户行可显示「部分成功」提示（可选，首版用 `ok` + 全局最近运行日志即可） |
| 飞书同步失败 | 本地 JSON 已保存则账户仍 `last_status=ok`；`last_error` 可记飞书警告文案或仅 slog；与现网「飞书失败不影响落盘」一致 |
| 抓取锁冲突 | 立即失败，不改各账户 `last_status` |

## 测试要点

- 账户 CRUD、MigrateFromConfig。
- 流水线双账户：其一 PrepCookies 失败 → skipped + UI 字段；另一仍 scrape/sync（mock）。
- 飞书字段与 upsert 键含「账号」。

## 实现触及的主要路径

- `internal/accounts`（新）
- `internal/web/server.go`、`templates/index.html`
- `internal/pipeline/board.go`、`internal/status`
- `internal/models`、`internal/statsstore`
- `internal/feishu/board_sync.go`
- `internal/auth`（按账户 Refresh / 配置视图）
- `.gitignore` 已忽略 `data/`，无需提交账户文件

## 成功标准

- UI 可添加 ≥2 账户，立即抓取对成功账户写入飞书且「账号」列正确区分。
- 失败账户在面板显示跳过及原因，不阻断其他账户。
- 旧单账号配置可无手工改 YAML 即迁入账户库。
