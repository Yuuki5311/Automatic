# Rod 无头浏览器迁移设计

**日期:** 2026-08-13  
**状态:** 已批准  
**语言:** Go  

## 背景

现有浏览器层基于 chromedp。登录时阿里百炼滑块在自动拖拽下常被行为风控拒绝；同时希望统一为无头浏览器，并去掉反检测相关改动。

## 目标

1. 用 `go-rod/rod` **整包替换** chromedp（`internal/browser`、`auth`、`captcha`、`scraper` 浏览器路径及依赖方）。
2. 浏览器默认 **无头**（`headless: true`）。
3. **不加反检测**：不注入 stealth、不改 `navigator.webdriver`、不设置 `disable-blink-features=AutomationControlled`、不伪造自定义 User-Agent。

## 非目标

- 有头手动拖滑块兜底
- 飞书 / 看板业务逻辑改动
- 第三方打码服务改造（接口可保留，实现随 page API 调整）

## 架构

```
Manager (rod.Browser + launcher)
    │
    ├─ NewContext(timeout) → context 携带 *rod.Page
    │       auth.PerformLogin / captcha.Solve / scraper 浏览器兜底
    └─ Close() → 关闭 Browser
```

- `PageFromContext(ctx)` 供业务包取当前页。
- Cookie：Rod `page.Cookies` / `SetCookies` 映射到现有 `models.CookieEntry`。
- 百炼滑块：iframe 内测量行程 + Rod `Mouse` 拖到最右；失败直接报错（无头无法人工）。

## 配置

- `browser.headless: true`（example 与本地 config）
- 保留 `chrome_path`、`timeout_sec`；`debug_port` 可选忽略或仅调试

## 约束

- 不加任何反检测 / stealth
- 无头为默认与主路径
- 移除对 `github.com/chromedp/*` 的直接依赖
