package models

import "time"

// RecycleOrder 回收订单统一数据模型
type RecycleOrder struct {
	OrderID      string    `json:"order_id"`      // 订单编号
	GameName     string    `json:"game_name"`     // 游戏名称
	ServerRegion string    `json:"server_region"` // 区服
	AccountInfo  string    `json:"account_info"`  // 账号信息摘要
	Price        float64   `json:"price"`         // 回收价格
	Status       string    `json:"status"`        // 订单状态
	CreateTime   time.Time `json:"create_time"`   // 创建时间
	CompleteTime time.Time `json:"complete_time"` // 完成时间
	BuyerInfo    string    `json:"buyer_info"`    // 买家信息
	Remarks      string    `json:"remarks"`       // 备注
	RawData      string    `json:"raw_data"`      // 原始数据JSON（防止丢失字段）
}

// CookieData Cookie 持久化格式
type CookieData struct {
	Cookies   []CookieEntry `json:"cookies"`
	UpdatedAt time.Time     `json:"updated_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

type CookieEntry struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"http_only"`
	Secure   bool    `json:"secure"`
}

// FeishuRecord 飞书多维表格记录
type FeishuRecord struct {
	RecordID string                 `json:"record_id"`
	Fields   map[string]interface{} `json:"fields"`
}

type BoardMetric struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Tips  string `json:"tips,omitempty"`
}

type GameBoardStats struct {
	GameName  string        `json:"game_name"`
	GameID    int           `json:"game_id"`
	TimeKey   string        `json:"time_key"`
	Metrics   []BoardMetric `json:"metrics"`
	FetchedAt time.Time     `json:"fetched_at"`
	RawJSON   string        `json:"raw_json,omitempty"`
	Error     string        `json:"error,omitempty"`
}

type BoardStatsSnapshot struct {
	Date      string           `json:"date"`
	ScrapedAt time.Time        `json:"scraped_at"`
	Games     []GameBoardStats `json:"games"`
}
