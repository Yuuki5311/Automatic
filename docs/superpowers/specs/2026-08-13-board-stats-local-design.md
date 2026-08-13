# 看板统计本地抓取设计

**日期:** 2026-08-13  
**状态:** 已批准  
**语言:** Go  

## 背景

原目标是抓取交易猫「我的回收」订单并写入飞书。经 API 验证，商户工作台看板接口 `mtop.com.jym.merchant.board.recyclestats` 可稳定返回各游戏昨日统计（通常 7 项，原神可能含「回收满意度」共 8 项）。第一阶段改为只抓该看板统计并落本地；飞书后续再接。

## 目标

1. 抓取 6 款游戏昨日看板统计并完整解析全部指标。
2. 结果写入本地 JSON，不写飞书。
3. UI 点击「获取 Cookie」可自动登录并保存 Cookie。
4. 进程常驻，每天 09:00 自动抓取一次；Cookie 失效时先自动登录，失败则停止本轮并在 UI 标红。
5. 提供 Go CLI：自动登录、单次/定时抓取。

## 非目标（本阶段）

- 飞书写入
- 回收订单列表 API / 浏览器 DOM 订单抓取
- `time` 除 `yesterday` 以外的取值
- 重做滑块验证码算法（沿用现有 captcha）

## 架构

```
cmd/gui（常驻 UI） 或  cmd/scraper -daemon
        │
        ├─ UI「获取 Cookie」→ auth.PerformLogin → data/cookies.json
        ├─ UI「立即抓取」  → scraper 看板抓取
        └─ cron 每天 09:00 → 同一抓取流程
                │
                ├─ Cookie 无效 → 自动登录；失败则停 + UI 标红
                ├─ MTOP recyclestats × 6 游戏（time=yesterday）
                └─ data/stats/YYYY-MM-DD.json（覆盖写）
```

辅助：

- `cmd/cookie-tool`：`-login` / `-import` / `-check`
- `cmd/scraper -once`：单次看板抓取落盘

飞书包保留，本阶段抓取路径不调用。

## 技术约束

- 全部实现使用 Go。
- MTOP 签名：`md5(token + "&" + t + "&" + appKey + "&" + data)`，`appKey=12574478`。
- 请求参数对齐抓包：`valueType=original`、`preventFallback=true`、`type=originaljson`。
- Cookie 关键字段含 `_m_h5_tk` / `_m_h5_tk_enc` 及 `ieu_member_*` / `jym_session_id` 等会话 Cookie。

## 游戏与 gameId

| 游戏 | gameId |
|------|--------|
| 火影忍者 | 1003132 |
| 原神 | 1009609 |
| 绝区零 | 1013597 |
| 崩坏：星穹铁道 | 2000334 |
| 鸣潮 | 2007615 |
| 三角洲行动 | 2007840 |

## 数据模型

```go
type BoardMetric struct {
    Title string // 咨询量、发起报价量…
    Value string // staData 原文
    Unit  string // "" | "元" | "%"
    Tips  string // properties.tips
}

type GameBoardStats struct {
    GameName  string
    GameID    int
    TimeKey   string // "yesterday"
    Metrics   []BoardMetric
    FetchedAt time.Time
    RawJSON   string
    Error     string // 单游戏失败时非空
}

type BoardStatsSnapshot struct {
    Date      string // 统计日：昨日 YYYY-MM-DD（本地时区）
    ScrapedAt time.Time
    Games     []GameBoardStats
}
```

主抓取路径不再以 `RecycleOrder` 为产出；订单相关类型可保留但本阶段不写入。

## 本地存储

- 路径：`data/stats/YYYY-MM-DD.json`
- `YYYY-MM-DD` = **昨日**日期（与接口 `time=yesterday` 对齐）
- 同日重复抓取：**覆盖**最新快照
- 权限：与现有 `data/` 一致，目录 gitignore

## 配置

默认调整（`configs/config.yaml.example` 同步）：

- `scraper.mode: "api"`（看板仅 MTOP，不走浏览器 DOM 订单页）
- `scraper.cron_expr: "0 9 * * *"`（每天 09:00）
- `scraper.games`：保留 6 游戏名；`table_count` 对本功能无意义，可忽略或保留兼容
- 新增可选：`scraper.stats_dir: "./data/stats"`（默认该路径）
- `jiaoyimao.username` / `password`：自动登录必需
- 飞书段保留占位，本阶段不校验、不调用

## 抓取流程

单次抓取（「立即抓取」与定时共用）：

1. 加载 Cookie；无效或不存在 → `PerformLogin`。
2. 登录失败 → `login_phase=failed`，本轮失败原因写入 status，**UI 标红**，**不发起统计请求**。
3. 登录成功 → 保存 Cookie，继续。
4. 对配置中每个游戏请求 `recyclestats`（`gameId` + `time=yesterday`）。
5. 单游戏 `ret` 非 SUCCESS：记录该游戏 `Error`，其余继续；若全部失败则整轮失败。
6. 组装 `BoardStatsSnapshot`，覆盖写入 `data/stats/昨日.json`。
7. 更新 `status.Store` 供 UI 轮询。

## Cookie 与 CLI

**UI「获取 Cookie（自动登录）」**

- 仅执行登录并保存，不抓统计。
- 必须绑定到真实 `PerformLogin`（修复 GUI 若未正确暴露 `TriggerLogin` 的问题）。
- 成功/失败通过 status 轮询展示。

**`cmd/cookie-tool`**

- `-login`：自动登录并写入 `cookie_path`
- `-import`：保留 EditThisCookie JSON 导入
- `-check`：校验本地 Cookie 是否有效

**`cmd/scraper`**

- `-once`：完整看板抓取落盘后退出
- `-daemon`：常驻 + cron `0 9 * * *`；可选 `-web`
- 启动时不强制立即抓取（可由 UI「立即抓取」触发）

**`cmd/gui`**

- 常驻窗口 + 内嵌仪表盘
- 进程内同样注册每天 09:00 cron（与 scraper daemon 行为一致，二选一运行即可）

## UI 展示

| 区域 | 行为 |
|------|------|
| Cookie 状态 | 有效/无效；登录失败红色 badge |
| Cookie 获取 | 自动登录按钮 + 可选手动导入 |
| 最近抓取 | 昨日日期、抓取时间、成功/失败 |
| 各游戏数据 | 展示 7～8 项看板指标（及单游戏错误） |
| 操作 | 「立即抓取」+ 文案「下次定时：每天 09:00」 |
| 守护状态 | 常驻显示「运行中」 |

## 错误处理

| 情况 | 行为 |
|------|------|
| Cookie 无效 | 尝试自动登录 |
| 自动登录失败 | 停止本轮；UI 标红；写日志 |
| 单游戏 API 失败 | 记入该游戏 Error；继续其他游戏 |
| 全部游戏失败 | 整轮失败状态 |
| 写盘失败 | 整轮失败；保留内存 status 中的本次结果（若有） |

## 测试要点

- 解析 fixtures：7 项与原神 8 项响应 → `GameBoardStats`
- 快照覆盖写：同日两次抓取只保留最新文件内容
- Cookie 无效路径：mock 登录失败 → 不调用 MTOP
- cron 表达式默认 `0 9 * * *` 可被配置覆盖
- `cookie-tool -check` 对已导入 Cookie 返回有效

## 实现顺序（供后续 plan）

1. 模型 + 本地存储读写
2. API：完整请求参数 + 解析 `recyclestats` → `GameBoardStats`
3. Manager 改为看板抓取编排（去掉本路径飞书）
4. status / UI 展示指标；修 GUI 登录绑定
5. cookie-tool `-login`；配置默认 cron / mode / stats_dir
6. gui 与 scraper daemon 挂载 09:00 定时
7. 单测与一次真实 `-once` 验证落盘
