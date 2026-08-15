package scraper

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// parseTableHTML 解析HTML中的表格数据。
//
// 兼容两类表格结构：
//   - 原生 <table><tbody><tr><td> 结构（跳过 thead 表头）
//   - Element UI 的 .el-table__body 结构（.el-table__cell 单元格）
//
// 单元格列顺序按交易猫回收页典型布局解析：
// 订单编号 | 账号信息 | 区服 | 价格 | 状态 | 时间 | 操作
func parseTableHTML(html string, gameName string) []models.RecycleOrder {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}

	var orders []models.RecycleOrder

	// 查找表格行（跳过表头）
	doc.Find("table tbody tr, .el-table__body tr").Each(func(i int, row *goquery.Selection) {
		cells := row.Find("td, .el-table__cell")
		if cells.Length() == 0 {
			return
		}

		order := models.RecycleOrder{
			GameName: gameName,
		}

		cells.Each(func(j int, cell *goquery.Selection) {
			text := strings.TrimSpace(cell.Text())
			switch j {
			case 0:
				order.OrderID = text
			case 1:
				order.AccountInfo = text
			case 2:
				order.ServerRegion = text
			case 3:
				order.Price = parsePrice(text)
			case 4:
				order.Status = text
			case 5:
				order.CreateTime = parseTime(text)
			case 6:
				order.Remarks = text
			}
		})

		orders = append(orders, order)
	})

	return orders
}

// parseJSONResponse 解析API返回的JSON数据。
//
// 支持两种常见响应结构：
//   - {code, message, data: {list|records: [...]}}
//   - {code, message, data: [...]}（data 直接是数组）
//
// 时间字段按字符串或Unix时间戳两种形式宽松解析，避免
// 站点返回 "2006-01-02 15:04:05" 这类非RFC3339格式导致整体解码失败。
func parseJSONResponse(body []byte, gameName string) ([]models.RecycleOrder, error) {
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析API响应失败: %w", err)
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("API业务错误: %s", envelope.Message)
	}

	orders := make([]models.RecycleOrder, 0)

	// 情形1: data 是对象 {list|records|total: ...}
	// 即使 list/records 为空数组或 null 也视为合法空结果（而非结构错误）
	var listObj struct {
		List    []recycleOrderJSON `json:"list"`
		Records []recycleOrderJSON `json:"records"`
		Total   int                `json:"total"`
	}
	if err := json.Unmarshal(envelope.Data, &listObj); err == nil && isListEnvelope(envelope.Data) {
		for _, item := range listObj.List {
			orders = append(orders, item.toOrder(gameName))
		}
		for _, item := range listObj.Records {
			orders = append(orders, item.toOrder(gameName))
		}
		return orders, nil
	}

	// 情形2: data 直接是数组
	var list []recycleOrderJSON
	if err := json.Unmarshal(envelope.Data, &list); err == nil {
		for _, item := range list {
			orders = append(orders, item.toOrder(gameName))
		}
		return orders, nil
	}

	return nil, fmt.Errorf("API响应结构无法识别: %s", string(envelope.Data))
}

// isListEnvelope 判断 data 对象是否含 list / records / total 任一字段，
// 避免把任意对象（如 {"foo": 1}）误判为合法的空订单列表。
func isListEnvelope(data json.RawMessage) bool {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return false
	}
	for _, k := range []string{"list", "records", "total"} {
		if _, ok := keys[k]; ok {
			return true
		}
	}
	return false
}

// flexibleTime 容忍多种时间格式的JSON时间字段。
type flexibleTime struct {
	time.Time
}

// UnmarshalJSON 支持三种时间表示：
//   - RFC3339 字符串（Go 标准格式，如 "2026-08-12T10:00:00+08:00"）
//   - 常见中文站点格式（见 parseTime，如 "2026-08-12 10:00:00"）
//   - Unix 时间戳（秒）
func (ft *flexibleTime) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		// 不是字符串，尝试 Unix 时间戳
		var ts float64
		if err2 := json.Unmarshal(data, &ts); err2 == nil && ts > 0 {
			ft.Time = time.Unix(int64(ts), 0)
			return nil
		}
		return err
	}
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		ft.Time = t
		return nil
	}
	ft.Time = parseTime(s)
	return nil
}

// recycleOrderJSON API响应中订单的宽松解码结构。
// 与 models.RecycleOrder 字段一致，但时间字段使用 flexibleTime 兼容多种格式。
type recycleOrderJSON struct {
	OrderID      string       `json:"order_id"`
	GameName     string       `json:"game_name"`
	ServerRegion string       `json:"server_region"`
	AccountInfo  string       `json:"account_info"`
	Price        float64      `json:"price"`
	Status       string       `json:"status"`
	CreateTime   flexibleTime `json:"create_time"`
	CompleteTime flexibleTime `json:"complete_time"`
	BuyerInfo    string       `json:"buyer_info"`
	Remarks      string       `json:"remarks"`
	RawData      string       `json:"raw_data"`
}

// toOrder 转换为统一的 models.RecycleOrder，gameName 参数
// 在接口未返回游戏名时兜底填充。
func (r recycleOrderJSON) toOrder(gameName string) models.RecycleOrder {
	order := models.RecycleOrder{
		OrderID:      r.OrderID,
		ServerRegion: r.ServerRegion,
		AccountInfo:  r.AccountInfo,
		Price:        r.Price,
		Status:       r.Status,
		CreateTime:   r.CreateTime.Time,
		CompleteTime: r.CompleteTime.Time,
		BuyerInfo:    r.BuyerInfo,
		Remarks:      r.Remarks,
		RawData:      r.RawData,
	}
	if r.GameName != "" {
		order.GameName = r.GameName
	} else {
		order.GameName = gameName
	}
	return order
}

// parsePrice 解析价格文本，支持货币符号前缀与千分位逗号。
// 解析失败时返回 0。
func parsePrice(text string) float64 {
	text = strings.TrimPrefix(text, "¥")
	text = strings.TrimPrefix(text, "￥")
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, ",", "") // 去掉千分位逗号
	price, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return price
}

// parseTime 解析常见的中文站点时间格式，失败时返回零值。
func parseTime(text string) time.Time {
	text = strings.TrimSpace(text)
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02",
		"01-02 15:04", // 月-日 时:分（同年默认）
		"2006年01月02日 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, text); err == nil {
			return t
		}
	}
	return time.Time{}
}

// normalizeGameName 将可能的游戏名变体统一化为标准名称。
func normalizeGameName(raw string) string {
	mapping := map[string]string{
		"火影忍者":       "火影忍者",
		"火影":         "火影忍者",
		"naruto":     "火影忍者",
		"原神":         "原神",
		"genshin":    "原神",
		"绝区零":        "绝区零",
		"zzz":        "绝区零",
		"崩坏：星穹铁道":    "崩坏：星穹铁道",
		"星穹铁道":       "崩坏：星穹铁道",
		"崩坏星穹铁道":     "崩坏：星穹铁道",
		"hsr":        "崩坏：星穹铁道",
		"鸣潮":         "鸣潮",
		"wuthering":  "鸣潮",
		"三角洲行动":      "三角洲行动",
		"deltaforce": "三角洲行动",
		"三角洲":        "三角洲行动",
		"和平精英":       "和平精英",
		"peace":      "和平精英",
		"王者荣耀":       "王者荣耀",
		"wangzhe":    "王者荣耀",
	}
	if standard, ok := mapping[raw]; ok {
		return standard
	}
	return raw
}
