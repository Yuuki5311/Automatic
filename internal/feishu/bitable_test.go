package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestOrderToFields(t *testing.T) {
	createTime := time.Date(2026, 8, 1, 10, 30, 0, 0, time.Local)
	b := NewBitableOps(&Client{}, "bascn_x") // 本测试不涉及网络请求
	order := models.RecycleOrder{
		OrderID:      "ORD-1001",
		GameName:     "原神",
		ServerRegion: "天空岛",
		AccountInfo:  "账号@example",
		Price:        1234.5,
		Status:       "回收中",
		CreateTime:   createTime,
		BuyerInfo:    "买家A",
		Remarks:      "测试备注",
	}

	fields := b.orderToFields(order)

	if got := fields[fieldOrderID]; got != "ORD-1001" {
		t.Errorf("订单编号 = %v, want ORD-1001", got)
	}
	if got := fields[fieldGameName]; got != "原神" {
		t.Errorf("游戏名称 = %v, want 原神", got)
	}
	if got := fields[fieldServerRegion]; got != "天空岛" {
		t.Errorf("区服 = %v, want 天空岛", got)
	}
	if got := fields[fieldAccountInfo]; got != "账号@example" {
		t.Errorf("账号信息 = %v, want 账号@example", got)
	}
	if got := fields[fieldPrice]; got != 1234.5 {
		t.Errorf("回收价格 = %v, want 1234.5", got)
	}
	if got := fields[fieldStatus]; got != "回收中" {
		t.Errorf("订单状态 = %v, want 回收中", got)
	}
	if got := fields[fieldCreateTime]; got != "2026-08-01 10:30:00" {
		t.Errorf("创建时间 = %v, want 2026-08-01 10:30:00", got)
	}
	if got := fields[fieldBuyerInfo]; got != "买家A" {
		t.Errorf("买家信息 = %v, want 买家A", got)
	}
	if got := fields[fieldRemarks]; got != "测试备注" {
		t.Errorf("备注 = %v, want 测试备注", got)
	}

	// 零值时间 → 空串
	if got := fields[fieldCompleteTime]; got != "" {
		t.Errorf("完成时间 = %q, want empty for zero time", got)
	}

	// 数据更新时间格式校验
	updated, ok := fields[fieldUpdatedAt].(string)
	if !ok {
		t.Fatalf("数据更新时间 type = %T, want string", fields[fieldUpdatedAt])
	}
	if _, err := time.Parse("2006-01-02 15:04:05", updated); err != nil {
		t.Errorf("数据更新时间 %q 格式非法: %v", updated, err)
	}

	// 字段完整性：11 个字段全部存在
	if len(fields) != 11 {
		t.Errorf("fields 数量 = %d, want 11", len(fields))
	}
}

// bitableMockServer 构造多维表格 mock 服务器：分页列表 + 批量创建 + 单条更新
func bitableMockServer(t *testing.T, tokenCalls, listCalls, createCalls, updateCalls *counter,
	createBodies, updateBodies *[][]byte, updatePaths *[]string,
	listHandler func(r *http.Request, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			tokenHandler(tokenCalls)(w, r)
		case "/bitable/v1/apps/" + testBitableID + "/tables/" + testTableID + "/records":
			if r.Method != http.MethodGet {
				http.Error(w, "unexpected method", http.StatusBadRequest)
				return
			}
			listCalls.Add()
			listHandler(r, w)
		case "/bitable/v1/apps/" + testBitableID + "/tables/" + testTableID + "/records/batch_create":
			createCalls.Add()
			body, _ := io.ReadAll(r.Body)
			*createBodies = append(*createBodies, body)
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"records":[{"record_id":"rec_new"}]}}`)
		default:
			if r.Method == http.MethodPut &&
				strings.HasPrefix(r.URL.Path,
					"/bitable/v1/apps/"+testBitableID+"/tables/"+testTableID+"/records/") {
				updateCalls.Add()
				*updatePaths = append(*updatePaths, r.URL.Path)
				body, _ := io.ReadAll(r.Body)
				*updateBodies = append(*updateBodies, body)
				fmt.Fprint(w, `{"code":0,"msg":"success"}`)
				return
			}
			http.NotFound(w, r)
		}
	}))
}

func TestBatchInsertOrdersNewAndUpdate(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			// 第一页：has_more + page_token；第二页：结束
			if r.URL.Query().Get("page_token") == "" {
				fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[
					{"record_id":"rec_old1","fields":{"订单编号":"A1","订单状态":"进行中"}},
					{"record_id":"rec_old2","fields":{"订单编号":"A2","订单状态":"进行中"}}
				],"has_more":true,"page_token":"p1"}}`)
			} else {
				fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[
					{"record_id":"rec_old3","fields":{"订单编号":"A3","订单状态":"进行中"}}
				],"has_more":false,"page_token":""}}`)
			}
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	b := NewBitableOps(c, testBitableID)

	orders := []models.RecycleOrder{
		{OrderID: "A1", GameName: "原神", Price: 100, Status: "已完成"}, // 已存在 → 更新
		{OrderID: "B1", GameName: "崩铁", Price: 200, Status: "进行中"}, // 新记录 → 新增
		{OrderID: "A3", GameName: "原神", Price: 300, Status: "已完成"}, // 已存在 → 更新
	}

	if err := b.BatchInsertOrders(context.Background(), testTableID, orders); err != nil {
		t.Fatalf("BatchInsertOrders: %v", err)
	}

	if got := listCalls.Get(); got != 2 {
		t.Errorf("list called %d times, want 2 (pagination)", got)
	}
	if got := createCalls.Get(); got != 1 {
		t.Errorf("batch_create called %d times, want 1", got)
	}

	// 新增记录请求体只包含 B1
	var createReq struct {
		Records []struct {
			Fields map[string]interface{} `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal(createBodies[0], &createReq); err != nil {
		t.Fatalf("parse create body: %v", err)
	}
	if len(createReq.Records) != 1 {
		t.Fatalf("create records = %d, want 1", len(createReq.Records))
	}
	if got := createReq.Records[0].Fields[fieldOrderID]; got != "B1" {
		t.Errorf("create 订单编号 = %v, want B1", got)
	}

	// 更新走 PUT，路径指向正确的 record_id
	if got := updateCalls.Get(); got != 2 {
		t.Errorf("PUT called %d times, want 2", got)
	}
	wantPaths := map[string]bool{
		"/bitable/v1/apps/" + testBitableID + "/tables/" + testTableID + "/records/rec_old1": true,
		"/bitable/v1/apps/" + testBitableID + "/tables/" + testTableID + "/records/rec_old3": true,
	}
	for _, p := range updatePaths {
		if !wantPaths[p] {
			t.Errorf("unexpected PUT path: %s", p)
		}
	}

	// 第一条更新是 A1（保持输入顺序）
	var updReq struct {
		Fields map[string]interface{} `json:"fields"`
	}
	if err := json.Unmarshal(updateBodies[0], &updReq); err != nil {
		t.Fatalf("parse update body: %v", err)
	}
	if got := updReq.Fields[fieldOrderID]; got != "A1" {
		t.Errorf("update 订单编号 = %v, want A1", got)
	}
}

func TestBatchInsertOrdersAllNew(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[],"has_more":false}}`)
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	b := NewBitableOps(c, testBitableID)

	orders := []models.RecycleOrder{
		{OrderID: "C1", GameName: "原神"},
		{OrderID: "C2", GameName: "崩铁"},
	}
	if err := b.BatchInsertOrders(context.Background(), testTableID, orders); err != nil {
		t.Fatalf("BatchInsertOrders: %v", err)
	}

	if createCalls.Get() != 1 {
		t.Errorf("batch_create called %d times, want 1", createCalls.Get())
	}
	var createReq struct {
		Records []struct {
			Fields map[string]interface{} `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal(createBodies[0], &createReq); err != nil {
		t.Fatalf("parse create body: %v", err)
	}
	if len(createReq.Records) != 2 {
		t.Errorf("create records = %d, want 2", len(createReq.Records))
	}
	if updateCalls.Get() != 0 {
		t.Errorf("PUT called %d times, want 0", updateCalls.Get())
	}
}

func TestBatchInsertOrdersAllExisting(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[
				{"record_id":"rec_x","fields":{"订单编号":"X1"}},
				{"record_id":"rec_y","fields":{"订单编号":"X2"}}
			],"has_more":false}}`)
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	b := NewBitableOps(c, testBitableID)

	orders := []models.RecycleOrder{
		{OrderID: "X1", GameName: "原神"},
		{OrderID: "X2", GameName: "原神"},
	}
	if err := b.BatchInsertOrders(context.Background(), testTableID, orders); err != nil {
		t.Fatalf("BatchInsertOrders: %v", err)
	}

	if createCalls.Get() != 0 {
		t.Errorf("batch_create called %d times, want 0 (all deduped)", createCalls.Get())
	}
	if updateCalls.Get() != 2 {
		t.Errorf("PUT called %d times, want 2", updateCalls.Get())
	}
}

func TestBatchInsertOrdersEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	b := NewBitableOps(c, testBitableID)
	if err := b.BatchInsertOrders(context.Background(), testTableID, nil); err != nil {
		t.Fatalf("BatchInsertOrders(nil): %v", err)
	}
	if err := b.BatchInsertOrders(context.Background(), testTableID, []models.RecycleOrder{}); err != nil {
		t.Fatalf("BatchInsertOrders(empty): %v", err)
	}
}

func TestBatchInsertOrdersChunking(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[],"has_more":false}}`)
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	b := NewBitableOps(c, testBitableID)

	// 覆盖 batch_create 分片：550 条 → 500 + 50 两批
	orders := make([]models.RecycleOrder, 550)
	for i := range orders {
		orders[i] = models.RecycleOrder{OrderID: fmt.Sprintf("B%04d", i), GameName: "原神"}
	}
	if err := b.BatchInsertOrders(context.Background(), testTableID, orders); err != nil {
		t.Fatalf("BatchInsertOrders: %v", err)
	}

	if got := createCalls.Get(); got != 2 {
		t.Fatalf("batch_create called %d times, want 2", got)
	}
	total := 0
	for _, body := range createBodies {
		var req struct {
			Records []map[string]interface{} `json:"records"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("parse create body: %v", err)
		}
		total += len(req.Records)
	}
	if total != 550 {
		t.Errorf("created total = %d, want 550", total)
	}
}

func TestBatchInsertOrdersListFailureInsertsAnyway(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			// 列表接口失败
			fmt.Fprint(w, `{"code":99999999,"msg":"list failed"}`)
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	b := NewBitableOps(c, testBitableID)

	orders := []models.RecycleOrder{{OrderID: "D1", GameName: "原神"}}
	if err := b.BatchInsertOrders(context.Background(), testTableID, orders); err != nil {
		t.Fatalf("BatchInsertOrders: %v", err)
	}
	// 文档化行为：拉取失败退化为全量新增，保证数据不丢
	if createCalls.Get() != 1 {
		t.Errorf("batch_create called %d times, want 1 (degrade to insert)", createCalls.Get())
	}
}

func TestListAllRecordsPagination(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			if r.URL.Query().Get("page_token") == "" {
				fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[
					{"record_id":"r1","fields":{"订单编号":"A1"}},
					{"record_id":"r2","fields":{"订单编号":"A2"}}
				],"has_more":true,"page_token":"p1"}}`)
			} else {
				fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[
					{"record_id":"r3","fields":{"订单编号":"A3"}}
				],"has_more":false,"page_token":""}}`)
			}
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	// 走 Client 便捷方法 ListRecords（使用配置中的 BitableID）
	records, err := c.ListRecords(context.Background(), testTableID)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	if records[2].RecordID != "r3" {
		t.Errorf("last record id = %s, want r3", records[2].RecordID)
	}
	if got, _ := records[0].Fields[fieldOrderID].(string); got != "A1" {
		t.Errorf("first record 订单编号 = %q, want A1", got)
	}
	if listCalls.Get() != 2 {
		t.Errorf("list called %d times, want 2", listCalls.Get())
	}
}

func TestListAllRecordsStopsOnRepeatedToken(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			// 服务端异常：has_more 一直为 true 且 page_token 不变 → 必须防死循环
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[],"has_more":true,"page_token":"stuck"}}`)
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	records, err := c.ListRecords(context.Background(), testTableID)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if got := listCalls.Get(); got != 2 {
		t.Errorf("list called %d times, want 2 (stopped on repeated token)", got)
	}
	if len(records) != 0 {
		t.Errorf("records = %d, want 0", len(records))
	}
}

func TestClientBatchInsertRecordsConvenience(t *testing.T) {
	var tokenCalls, listCalls, createCalls, updateCalls counter
	var createBodies, updateBodies [][]byte
	var updatePaths []string

	srv := bitableMockServer(t, &tokenCalls, &listCalls, &createCalls, &updateCalls,
		&createBodies, &updateBodies, &updatePaths,
		func(r *http.Request, w http.ResponseWriter) {
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[],"has_more":false}}`)
		})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	// 便捷方法：BitableID 取自 cfg.BitableID（newTestClient 注入）
	if err := c.BatchInsertRecords(context.Background(), testTableID,
		[]models.RecycleOrder{{OrderID: "E1", GameName: "原神"}}); err != nil {
		t.Fatalf("BatchInsertRecords: %v", err)
	}
	if createCalls.Get() != 1 {
		t.Errorf("batch_create called %d times, want 1", createCalls.Get())
	}
}
