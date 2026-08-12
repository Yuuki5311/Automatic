package scraper

import (
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// ---------- parsePrice ----------

func TestParsePrice(t *testing.T) {
	tests := []struct {
		name string
		text string
		want float64
	}{
		{"plain number", "123.45", 123.45},
		{"yen symbol", "¥1,234.5", 1234.5},
		{"fullwidth yen symbol", "￥99", 99},
		{"thousands separator", "12,345,678", 12345678},
		{"trailing spaces", " 45.6 ", 45.6},
		{"empty", "", 0},
		{"not a number", "--", 0},
		{"text garbage", "面议", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePrice(tt.text); got != tt.want {
				t.Fatalf("parsePrice(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// ---------- parseTime ----------

func TestParseTime(t *testing.T) {
	// time.Parse 对不含时区的布局返回 UTC（跨机器结果确定，不依赖本机时区）
	tests := []struct {
		name string
		text string
		want time.Time
	}{
		{"full datetime", "2026-08-12 10:30:00", time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)},
		{"date only", "2026-08-12", time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)},
		{"slash format", "2026/08/12 10:30:00", time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)},
		{"slash date only", "2026/08/12", time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)},
		{"month-day with time", "08-12 10:30", time.Date(0, 8, 12, 10, 30, 0, 0, time.UTC)},
		{"chinese format", "2026年08月12日 10:30:00", time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)},
		{"minute precision", "2026-08-12 10:30", time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)},
		{"empty", "", time.Time{}},
		{"garbage", "not a time", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTime(tt.text)
			if !got.Equal(tt.want) {
				t.Fatalf("parseTime(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// ---------- normalizeGameName ----------

func TestNormalizeGameName(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"原神", "原神"},
		{"genshin", "原神"},
		{"火影", "火影忍者"},
		{"naruto", "火影忍者"},
		{"崩坏星穹铁道", "崩坏：星穹铁道"},
		{"hsr", "崩坏：星穹铁道"},
		{"zzz", "绝区零"},
		{"三角洲", "三角洲行动"},
		{"deltaforce", "三角洲行动"},
		{"wuthering", "鸣潮"},
		{"未知游戏X", "未知游戏X"}, // 无映射时原样返回
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := normalizeGameName(tt.raw); got != tt.want {
				t.Fatalf("normalizeGameName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// ---------- parseTableHTML ----------

const sampleTableHTML = `
<html><body>
<table>
  <thead>
    <tr>
      <th>订单编号</th><th>账号信息</th><th>区服</th><th>价格</th><th>状态</th><th>时间</th><th>操作</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td>NO202608120001</td>
      <td>账号A</td>
      <td>官服</td>
      <td>¥1,299.00</td>
      <td>回收中</td>
      <td>2026-08-12 10:30:00</td>
      <td>查看</td>
    </tr>
    <tr>
      <td>NO202608120002</td>
      <td>账号B</td>
      <td>B服</td>
      <td>88.5</td>
      <td>已完成</td>
      <td>2026-08-11 09:00:00</td>
      <td>查看</td>
    </tr>
  </tbody>
</table>
</body></html>
`

func TestParseTableHTML_PlainTable(t *testing.T) {
	orders := parseTableHTML(sampleTableHTML, "原神")
	if len(orders) != 2 {
		t.Fatalf("parseTableHTML returned %d orders, want 2", len(orders))
	}

	first := orders[0]
	if first.OrderID != "NO202608120001" {
		t.Errorf("OrderID = %q, want NO202608120001", first.OrderID)
	}
	if first.AccountInfo != "账号A" {
		t.Errorf("AccountInfo = %q, want 账号A", first.AccountInfo)
	}
	if first.ServerRegion != "官服" {
		t.Errorf("ServerRegion = %q, want 官服", first.ServerRegion)
	}
	if first.Price != 1299.00 {
		t.Errorf("Price = %v, want 1299", first.Price)
	}
	if first.Status != "回收中" {
		t.Errorf("Status = %q, want 回收中", first.Status)
	}
	if first.GameName != "原神" {
		t.Errorf("GameName = %q, want 原神", first.GameName)
	}
	wantTime := time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
	if !first.CreateTime.Equal(wantTime) {
		t.Errorf("CreateTime = %v, want %v", first.CreateTime, wantTime)
	}

	// 表头不能被当成数据行
	if orders[0].OrderID == "订单编号" {
		t.Fatal("header row was parsed as a data row")
	}
}

func TestParseTableHTML_ElTable(t *testing.T) {
	elHTML := `
<div class="el-table">
  <div class="el-table__body">
    <table><tbody>
      <tr>
        <td class="el-table__cell">NO1001</td>
        <td class="el-table__cell">账号C</td>
        <td class="el-table__cell">官服</td>
        <td class="el-table__cell">¥66.6</td>
        <td class="el-table__cell">待处理</td>
        <td class="el-table__cell">2026/08/12</td>
        <td class="el-table__cell">操作</td>
      </tr>
    </tbody></table>
  </div>
</div>`
	orders := parseTableHTML(elHTML, "火影忍者")
	if len(orders) != 1 {
		t.Fatalf("parseTableHTML(el-table) returned %d orders, want 1", len(orders))
	}
	if orders[0].OrderID != "NO1001" || orders[0].Price != 66.6 {
		t.Fatalf("el-table row parsed incorrectly: %+v", orders[0])
	}
}

func TestParseTableHTML_EmptyAndInvalid(t *testing.T) {
	if orders := parseTableHTML("", "原神"); orders != nil && len(orders) != 0 {
		t.Fatalf("empty HTML should produce no orders, got %d", len(orders))
	}
	// 只有表头没有数据行
	headerOnly := "<table><thead><tr><th>编号</th></tr></thead><tbody></tbody></table>"
	if orders := parseTableHTML(headerOnly, "原神"); len(orders) != 0 {
		t.Fatalf("header-only table should produce no orders, got %d", len(orders))
	}
	// 非HTML内容
	if orders := parseTableHTML("not html <<<", "原神"); orders != nil {
		t.Fatalf("invalid HTML should return nil, got %+v", orders)
	}
}

// ---------- parseJSONResponse ----------

func TestParseJSONResponse_ListEnvelope(t *testing.T) {
	body := `{
		"code": 0,
		"message": "ok",
		"data": {
			"total": 2,
			"list": [
				{
					"order_id": "NO2001",
					"game_name": "原神",
					"server_region": "官服",
					"account_info": "账号X",
					"price": 199.5,
					"status": "回收中",
					"create_time": "2026-08-12 10:30:00",
					"complete_time": "2026-08-13T11:00:00+08:00",
					"buyer_info": "买家1",
					"remarks": "备注"
				},
				{
					"order_id": "NO2002",
					"create_time": 1786695000
				}
			]
		}
	}`

	orders, err := parseJSONResponse([]byte(body), "兜底游戏")
	if err != nil {
		t.Fatalf("parseJSONResponse failed: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("got %d orders, want 2", len(orders))
	}

	first := orders[0]
	if first.OrderID != "NO2001" || first.Price != 199.5 || first.BuyerInfo != "买家1" {
		t.Errorf("first order fields mismatch: %+v", first)
	}
	// 站点常用格式 "2006-01-02 15:04:05" 应被宽松解析
	if !first.CreateTime.Equal(time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("CreateTime = %v, want 2026-08-12 10:30:00", first.CreateTime)
	}
	// RFC3339 格式
	if !first.CompleteTime.Equal(time.Date(2026, 8, 13, 11, 0, 0, 0, time.FixedZone("", 8*3600))) {
		t.Errorf("CompleteTime = %v, want 2026-08-13 11:00:00+08:00", first.CompleteTime)
	}

	// 第二个订单未返回 game_name，应兜底填充传入的游戏名
	if orders[1].GameName != "兜底游戏" {
		t.Errorf("fallback GameName = %q, want 兜底游戏", orders[1].GameName)
	}
	// Unix 时间戳形式
	if !orders[1].CreateTime.Equal(time.Unix(1786695000, 0)) {
		t.Errorf("CreateTime(unix) = %v, want %v", orders[1].CreateTime, time.Unix(1786695000, 0))
	}
}

func TestParseJSONResponse_RecordsEnvelope(t *testing.T) {
	body := `{"code": 0, "data": {"records": [{"order_id": "R1"}]}}`
	orders, err := parseJSONResponse([]byte(body), "火影忍者")
	if err != nil {
		t.Fatalf("parseJSONResponse(records) failed: %v", err)
	}
	if len(orders) != 1 || orders[0].OrderID != "R1" || orders[0].GameName != "火影忍者" {
		t.Fatalf("records envelope parsed incorrectly: %+v", orders)
	}
}

func TestParseJSONResponse_EmptyListEnvelope(t *testing.T) {
	// 空列表（含 null list）应视为合法空结果，而不是结构错误
	for _, body := range []string{
		`{"code": 0, "data": {"list": [], "total": 0}}`,
		`{"code": 0, "data": {"list": null, "total": 0}}`,
		`{"code": 0, "data": {"records": []}}`,
	} {
		orders, err := parseJSONResponse([]byte(body), "原神")
		if err != nil {
			t.Fatalf("parseJSONResponse(%s) failed: %v", body, err)
		}
		if len(orders) != 0 {
			t.Fatalf("parseJSONResponse(%s) returned %d orders, want 0", body, len(orders))
		}
	}
}

func TestParseJSONResponse_DirectArray(t *testing.T) {
	body := `{"code": 0, "data": [{"order_id": "A1"}, {"order_id": "A2"}]}`
	orders, err := parseJSONResponse([]byte(body), "鸣潮")
	if err != nil {
		t.Fatalf("parseJSONResponse(array) failed: %v", err)
	}
	if len(orders) != 2 || orders[0].OrderID != "A1" || orders[1].OrderID != "A2" {
		t.Fatalf("direct array parsed incorrectly: %+v", orders)
	}
}

func TestParseJSONResponse_BusinessError(t *testing.T) {
	body := `{"code": 40100, "message": "请先登录", "data": null}`
	if _, err := parseJSONResponse([]byte(body), "原神"); err == nil {
		t.Fatal("business error should return error")
	} else if !strings.Contains(err.Error(), "请先登录") {
		t.Fatalf("error should carry server message, got: %v", err)
	}
}

func TestParseJSONResponse_Invalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", "not json at all"},
		{"unknown structure", `{"code": 0, "data": {"foo": 1}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseJSONResponse([]byte(tt.body), "原神"); err == nil {
				t.Fatalf("parseJSONResponse(%q) should error", tt.body)
			}
		})
	}
}

// ---------- toOrder ----------

func TestToOrder_EmptyGameNameFallback(t *testing.T) {
	item := recycleOrderJSON{OrderID: "X1", Price: 10}
	order := item.toOrder("原神")
	if order.GameName != "原神" {
		t.Fatalf("GameName = %q, want 原神", order.GameName)
	}
	// 接口显式返回的游戏名优先
	item.GameName = "genshin"
	if order := item.toOrder("原神"); order.GameName != "genshin" {
		t.Fatalf("GameName = %q, want genshin (API wins over fallback)", order.GameName)
	}
}

// ---------- 与 models 的兼容性 ----------

func TestRecycleOrderModelCompatibility(t *testing.T) {
	// 确保解析结果可直接作为 models.RecycleOrder 使用
	orders := parseTableHTML(sampleTableHTML, "原神")
	var _ []models.RecycleOrder = orders
	if len(orders) == 0 {
		t.Fatal("expected parsed orders")
	}
}
