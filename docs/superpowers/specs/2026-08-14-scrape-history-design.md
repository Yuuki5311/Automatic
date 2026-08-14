# 抓取操作历史（最近抓取按日/账号）设计

## 目标

将 UI「最近抓取」从「仅展示最近一整轮汇总」改为**按日期分组、按账号分行**的操作记录，并可跨重启查看最近 **30 天**。

## 非目标

- 不记录未实际执行的账号（未启用 / 本轮未跑到）
- 不在历史行内展示各游戏指标明细（精简字段）
- 不引入 SQLite 或其他数据库
- 不改变飞书同步与看板 JSON 落盘逻辑

## 需求确认

| 项 | 选择 |
|----|------|
| 粒度 | 每账号每轮一行 |
| 保留 | 30 天 |
| 字段 | 时间、账号、状态、错误摘要（可选） |
| 展示 | 按日期分组，组内按账号分开 |

## 数据模型

```go
type HistoryEntry struct {
    ID        string    `json:"id"`
    At        time.Time `json:"at"`          // 该账号本轮结束时间
    Account   string    `json:"account"`     // mobile / username
    Status    string    `json:"status"`      // ok | skipped | error
    Error     string    `json:"error,omitempty"`
}
```

- `Status` 与账户库 `LastStatus` 对齐：`ok` / `skipped`；若将来出现明确失败态可用 `error`，否则跳过失败统一记 `skipped` 并把原因写入 `Error`。
- `ID`：短随机 id，便于调试；非必须被 UI 使用。

## 持久化

- 路径：与账户库同级，默认 `data/scrape_history.json`（由 `accounts.PathFromCookie` 同目录推导，或显式 `filepath.Join(dir(accounts.json), "scrape_history.json")`）。
- 文件形态：

```json
{
  "entries": [ /* 新→旧 或 无序均可，读入后排序 */ ]
}
```

- 写入：每个账号 `runAccount` 结束后追加一条，然后 `prune` 掉 `At < now-30d` 的条目并保存。
- 并发：与 `accounts.Store` 类似，包内 `sync.Mutex`；抓取串行时冲突少，仍需加锁以支持 UI 轮询并发读。
- 损坏/缺失：视为空列表，不阻断抓取。

## 状态与 API

- `status.Snapshot` 增加 `ScrapeHistory []HistoryEntry`（或按日分组的 DTO；见下）。
- 启动 Web / scraper 时加载 history store，抓取写入后刷新 snapshot。
- `GET /api/status` 与首页模板一并带上历史；可不新增独立 API（YAGNI）。若条目过多导致 status 偏大：UI 仍只取 30 天内全量（账号数 × 天通常可接受）。

### UI 分组 DTO（可选）

服务端或模板侧按本地日历日分组即可：

```text
今天 (YYYY-MM-DD)
  account  HH:MM  status  error?
昨天 (...)
更早日期标题用 YYYY-MM-DD
```

组内按 `At` 降序；日期组本身也降序（今天最上）。

## UI 变更

「最近抓取」区块：

1. 若 `CurrentRun != nil`：顶部保留一行「进行中 + 开始时间」。
2. 下方为按日分组的账号历史表/列表；无记录时显示「暂无抓取记录」。
3. 去掉或大幅弱化原整轮汇总（统计日期/耗时/成功跳过数）——**以账号历史为主**；整轮 `LastRun` 可仍写入 status 供其它逻辑，但 UI 不再作为主展示。

「各游戏数据」仍展示**最近一次成功/部分成功轮次**的游戏指标（现有行为），与历史列表职责分离。

## 写入点

`internal/pipeline/board.go` 的 `runAccount`：在 `UpdateStatus` 之后调用 `history.Append(...)`。

- 成功：`status=ok`，`error=""`
- Cookie/抓取失败跳过：`status=skipped`，`error=原错误字符串`（可截断至合理长度，如 200 字）
- 因登录页自动禁用：仍记一条 `skipped`（错误里已有禁用原因）

`gui` / `scraper` 启动时构造 history store 并注入 `BoardRun`（或通过回调/`History` 接口字段注入，便于测试）。

## 测试

- history store：追加、30 天裁剪、空文件/坏 JSON、并发读追加烟雾。
- pipeline：mock 双账号一轮，断言产生 2 条 history（成功 + 跳过）。
- web：首页/`/api/status` 含 history 字段；模板含按日分组文案或结构。

## 验收

1. 跑一轮多账号后，UI「最近抓取」按今天日期列出各账号时间与结果。
2. 次日（或把系统日期/条目时间 stub 到昨天）可见「昨天」分组。
3. 重启进程后历史仍在。
4. 超过 30 天的条目不再出现在文件与 UI。
