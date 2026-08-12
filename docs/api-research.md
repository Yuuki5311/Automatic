# 交易猫商户平台 API 反向工程记录

> 本文档是 API 反向工程的**工作手册**：既是抓包操作指南，也是最终记录沉淀的位置。
> 当前状态：**模板与推测端点已就绪，真实端点待抓包确认**（见各节「待确认」标记）。
> 抓包完成后，用下文「抓包记录模板」逐条记录，并同步更新本文档与 `configs/config.yaml` / `internal/scraper/api.go`。

---

## 1. 抓包方法

### 1.1 准备工作

1. 使用**无痕窗口**（Ctrl+Shift+N）打开 `https://merchant.jiaoyimao.com/workbench`，避免缓存与已登录会话干扰抓包。
2. 打开 Chrome DevTools：按 **F12**（或右键 → 检查）。
3. 切换到 **Network（网络）** 标签页。
4. **勾选 "Preserve log"（保留日志）** —— 防止页面跳转/刷新后记录被清空。
5. 在过滤器输入框中输入 `XHR`，或点击 **Fetch/XHR** 筛选按钮，只看接口请求（忽略 JS/CSS/图片）。

### 1.2 抓取步骤

| 步骤 | 操作 | 目标 |
|------|------|------|
| 1 | 登录商户平台 | 抓取登录 API（URL / 请求体 / 响应 token 字段名） |
| 2 | 导航到「我的回收」页面 | 抓取回收订单列表 API |
| 3 | 依次切换 6 个游戏标签 | 记录每个游戏的 `game_id` 参数值 |
| 4 | 翻页 / 切换「全部 / 进行中 / 已完成」等状态筛选 | 确定分页与状态参数 |
| 5 | 点击展开订单详情 | 抓取详情 API（可选） |

### 1.3 记录请求的 4 种方式

- **Copy as cURL**：右键请求 → Copy → Copy as cURL，可完整还原请求（含 Cookie、Headers），用 `curl` 命令直接复测。
- **Copy as fetch**：右键请求 → Copy → Copy as fetch，可在控制台里用浏览器上下文重放（自动带登录态）。
- **查看 Headers**：点击请求 → Headers 面板，记录 Request URL、Request Method、Query String Parameters、Request Headers（重点关注 `Authorization` / `Cookie` / `X-Requested-With` / `Content-Type`）。
- **查看 Payload**：点击请求 → Payload 面板，记录请求体 JSON 结构与字段含义。
- **查看 Response**：点击请求 → Response / Preview 面板，记录响应 JSON 结构。

### 1.4 技巧与注意事项

- 接口路径通常含 `/api/`，可在过滤器里直接输入 `api` 快速定位。
- 如果某些请求看不到响应体（已跳转导致请求被取消），勾选 Preserve log 后刷新页面再操作。
- 登录态一般通过 **Cookie** 或 **Authorization: Bearer <token>** 传递，两者都要记录（当前代码两种均兼容，见 §3.3）。
- 把抓到的 URL 存成书签或粘贴到本文档 §5 模板中，避免丢失。

---

## 2. 已发现的 API 端点

### 2.1 登录相关（推测模式，待确认）

- **POST** `https://merchant.jiaoyimao.com/api/v1/login`
  - 参数（请求体 JSON）: `phone`, `password`, `captcha_token`
  - 响应: `{ "code": 0, "data": { "token": "xxx" } }`
  - 待确认：实际登录 URL、`captcha_token` 获取方式、token 字段名（`token` / `access_token` / `jwt` 等）

### 2.2 回收订单列表（推测模式，待确认）

- **GET** `https://merchant.jiaoyimao.com/api/v1/merchant/recycle/orders?game_id=xxx&page=1&page_size=50`
  - Headers: `Authorization: Bearer xxx`（或 Cookie 透传）
  - 待确认：`game_id` / `game` / `game_name` 参数名；分页参数名（`page`/`pageNum`、`page_size`/`pageSize`）；是否携带 `table` 参数（原神、星铁有两个回收表格）

> 代码中当前的推测实现（`internal/scraper/api.go` 注释明确标注"需根据实际抓包结果替换"）：
> `GET /api/v1/merchant/recycle/orders?game=<游戏名>&table=<0/1>&page=1&pageSize=500`
> 抓包后请以此为准核对修正。

### 2.3 【待补充】

- 各游戏对应的 `game_id` 映射（见 §3）
- 表格分页参数的真实名称与总页数/总数返回字段
- 具体响应字段映射（见 §4）
- 登录后 Cookie / Token 的刷新机制

---

## 3. 游戏与 game_id 映射

游戏列表来自 `configs/config.yaml` 的 `scraper.games`。`game_id` 为抓包后填写，未确认前保持「待抓包」。

| 游戏名（配置用名） | 「我的回收」页面路径 | 回收表格数 | game_id | 抓包状态 |
|---|---|---|---|---|
| 火影忍者 | `/workbench/recycle/naruto` | 1 | 待抓包 | 未确认 |
| 原神 | `/workbench/recycle/genshin` | 2（官服 / 渠道服B服） | 待抓包 | 未确认 |
| 绝区零 | `/workbench/recycle/zzz` | 1 | 待抓包 | 未确认 |
| 崩坏：星穹铁道 | `/workbench/recycle/hsr` | 2（官服 / 渠道服） | 待抓包 | 未确认 |
| 鸣潮 | `/workbench/recycle/wuthering` | 1 | 待抓包 | 未确认 |
| 三角洲行动 | `/workbench/recycle/deltaforce` | 1 | 待抓包 | 未确认 |

填写说明：

- 切换游戏标签时，列表 API 请求中与游戏相关的查询参数（`game_id` / `game` / `game_name` / `gameCode` 等）即该游戏的取值。
- 抓包时对照表格逐行填写 `game_id` 列，并将「抓包状态」改为「已确认」。
- 原神 / 星铁各有 2 个表格，若两个表格请求的 `game_id` 相同，注意记录区分它们的附加参数（如 `table` / `server_type`）。

---

## 4. 响应 JSON 结构

### 4.1 外层包裹（代码已兼容两种形态，见 `internal/scraper/api.go` 的 `parseJSONResponse`）

形态 A —— 数据在 `data.list`：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "list": [
      { "...订单对象..." }
    ],
    "total": 123,
    "page": 1
  }
}
```

形态 B —— 数据直接是数组：

```json
{
  "code": 0,
  "message": "success",
  "data": [ { "...订单对象..." } ]
}
```

> 待确认：`code` 的取值约定（0=成功？）、分页/总数返回字段名。

### 4.2 订单字段映射（与 `internal/models/models.go` 的 `RecycleOrder` 对应）

| 代码字段 (Go) | JSON key | 含义 | 备注（按抓包结果填写） |
|---|---|---|---|
| `OrderID` | `order_id` | 订单编号 | 待确认实际 key |
| `GameName` | `game_name` | 游戏名称 | 待确认实际 key |
| `ServerRegion` | `server_region` | 区服 | 待确认实际 key |
| `AccountInfo` | `account_info` | 账号信息摘要 | 待确认实际 key |
| `Price` | `price` | 回收价格 | 注意数值类型（number/string） |
| `Status` | `status` | 订单状态 | 待确认枚举值含义 |
| `CreateTime` | `create_time` | 创建时间 | 注意时间格式（RFC3339/时间戳） |
| `CompleteTime` | `complete_time` | 完成时间 | 同上 |

---

## 5. 抓包记录模板

每确认一个端点，复制以下模板填写并归档到本文档 §2 对应小节。

```markdown
### 端点名称：<登录 / 订单列表 / 订单详情 ...>

- **抓包日期**：YYYY-MM-DD
- **完整 URL**：<method> https://host/path?query
- **请求方法**：GET / POST / PUT ...

**请求头：**
```http
Authorization: Bearer <token 或留空>
Cookie: <如有>
Content-Type: application/json
X-Requested-With: <如有>
```

**请求体（POST 时填写）：**
```json
{ "字段": "说明" }
```

**响应 JSON（脱敏）：**
```json
{ "完整结构粘贴" }
```

**字段含义对照表：**

| 响应字段 | 类型 | 含义 | 对应代码字段 |
|---|---|---|---|
| `xxx` | string | xxx | `OrderID` |

**验证结果**：curl 复测通过 / 失败（附原因）｜ 已同步 `configs/config.yaml` / `internal/scraper/api.go`
```

---

## 6. 验证与落地清单

完成抓包后按此清单逐项核对（对应计划 Step 2 / Step 3）：

- [ ] 登录 API：URL、请求体、token 字段名已记录并可在 curl 中复测
- [ ] 回收订单列表 API：真实 URL 已记录
- [ ] 6 个游戏标签的 `game_id`（或等价参数）已填入 §3 表格
- [ ] 分页参数名与取值（`page` / `page_size` 或 `page` / `pageSize`）已确认
- [ ] 响应 JSON 结构与订单字段 key 已确认，§4.2 表格已填写
- [ ] 鉴权方式已确认（Cookie 透传 or `Authorization: Bearer`）
- [ ] 已更新 `internal/scraper/api.go` 中的 API URL 与查询参数（当前为推测实现）
- [ ] 已更新 `configs/config.yaml` 中游戏页面路径（如需）
- [ ] `go build ./...` 通过，`go test ./...` 通过
- [ ] 已提交：`git commit -m "docs: update API research findings"`
