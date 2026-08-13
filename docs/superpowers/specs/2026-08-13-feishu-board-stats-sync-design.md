# 看板统计写入飞书多维表格设计

**日期:** 2026-08-13  
**状态:** 已批准  
**语言:** Go  

## 背景

看板统计已落盘本地 JSON。需同步写入飞书多维表格；目标表当前无列，需由程序创建字段后再写记录。

## 目标

1. 每个游戏每天一行（upsert：同「日期+游戏名称」则更新）。
2. `app_token=GzGfb0UECaWsjPsiEgScFnqqnQn`，`table_id=tbl9GnWtSkuMzIoV`。
3. 表无列时自动创建所需字段。
4. 抓取成功落盘后写入飞书；飞书失败不影响本地 JSON。

## 非目标

- 使用 `app/update` 写数据（该接口只改应用名）。
- 按游戏分多张表（旧 `table_mapping` 订单方案本阶段不用）。
- 有头手动滑块。

## 字段

| 字段名 | 类型 |
|--------|------|
| 日期 | 日期 |
| 游戏名称 | 文本 |
| 咨询量 | 数字 |
| 带看量 | 数字 |
| 回收成功订单数 | 数字 |
| 回收成功金额 | 数字 |
| 回收成功率 | 数字 |
| 回收满意度 | 数字 |
| 回收主页咨询量 | 数字 |
| 回收主页成功订单数 | 数字 |
| 抓取时间 | 文本 |
| 错误信息 | 文本 |

指标从 `GameBoardStats.Metrics` 按 `title` 映射；缺失指标留空。

## 架构

```
pipeline.BoardRun
  → scrape + statsstore.Save
  → feishu.BitableOps.SyncBoardStats(snapshot)   // EnsureFields → list → upsert
```

API：
- 建字段：`POST .../apps/{app_token}/tables/{table_id}/fields`
- 写记录：`batch_create` / 单条 `PUT .../records/{record_id}`
- 读记录：分页 `GET .../records`

## 配置

```yaml
feishu:
  app_id: "..."
  app_secret: "..."
  bitable_id: "GzGfb0UECaWsjPsiEgScFnqqnQn"   # app_token
  board_table_id: "tbl9GnWtSkuMzIoV"
```

应用需具备多维表格读写权限，并已添加该 Base。
