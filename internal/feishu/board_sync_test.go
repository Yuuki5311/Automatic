package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestBoardGameToFields(t *testing.T) {
	snap := models.BoardStatsSnapshot{
		Date:      "2026-08-12",
		ScrapedAt: time.Date(2026, 8, 13, 9, 0, 0, 0, time.Local),
	}
	g := models.GameBoardStats{
		GameName: "原神",
		Metrics: []models.BoardMetric{
			{Title: "咨询量", Value: "186"},
			{Title: "回收成功金额", Value: "9650.00"},
			{Title: "回收成功率", Value: "10.22"},
		},
	}
	fields := boardGameToFields(snap, g)
	if fields[boardFieldGame] != "原神" {
		t.Fatalf("game = %v", fields[boardFieldGame])
	}
	if fields[boardFieldConsult] != float64(186) {
		t.Fatalf("consult = %v", fields[boardFieldConsult])
	}
	if fields[boardFieldAmount] != 9650.0 {
		t.Fatalf("amount = %v", fields[boardFieldAmount])
	}
	ms, ok := fields[boardFieldDate].(int64)
	if !ok || ms <= 0 {
		t.Fatalf("date = %v", fields[boardFieldDate])
	}
	if boardDateKey(float64(ms)) != "2026-08-12" {
		t.Fatalf("date key = %s", boardDateKey(float64(ms)))
	}
}

func TestBoardGameToFieldsMapsQuoteMetric(t *testing.T) {
	snap := models.BoardStatsSnapshot{Date: "2026-08-12", Account: "138", ScrapedAt: time.Now()}
	g := models.GameBoardStats{
		GameName: "原神",
		Metrics: []models.BoardMetric{
			{Title: "发起报价量", Value: "42"},
			{Title: "咨询量", Value: "187"},
		},
	}
	f := boardGameToFields(snap, g)
	if f[boardFieldQuote] != float64(42) {
		t.Fatalf("发起报价量 = %v", f[boardFieldQuote])
	}
	if f[boardFieldConsult] != float64(187) {
		t.Fatalf("咨询量 = %v", f[boardFieldConsult])
	}
}

func TestBoardGameToFieldsIncludesAccount(t *testing.T) {
	snap := models.BoardStatsSnapshot{Date: "2026-08-12", Account: "13800000000", ShopName: "熊熊店", UID: "1727867117436205", ScrapedAt: time.Now()}
	g := models.GameBoardStats{GameName: "原神"}
	f := boardGameToFields(snap, g)
	if f[boardFieldAccount] != "13800000000" {
		t.Fatalf("%v", f)
	}
	if f[boardFieldShopName] != "熊熊店" {
		t.Fatalf("shop=%v", f[boardFieldShopName])
	}
	if f[boardFieldUID] != "1727867117436205" {
		t.Fatalf("uid=%v", f[boardFieldUID])
	}
}

func TestBoardRecordKey(t *testing.T) {
	if got := boardRecordKey("2026-08-12", "138", "原神"); got != "2026-08-12|138|原神" {
		t.Fatal(got)
	}
}

func TestParseMetricNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"186", 186, true},
		{"9,650.00", 9650, true},
		{"10.22%", 10.22, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMetricNumber(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseMetricNumber(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSyncBoardStatsHTTP(t *testing.T) {
	var createdFields []string
	var createdN int

	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "tenant_access_token": "t-test", "expire": 7200,
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/fields", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0, "data": map[string]interface{}{"items": []any{}, "has_more": false},
			})
		case http.MethodPost:
			var req struct {
				FieldName string `json:"field_name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			createdFields = append(createdFields, req.FieldName)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
		}
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0, "data": map[string]interface{}{"items": []any{}, "has_more": false},
			})
		}
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/batch_create", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Records []any `json:"records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		createdN += len(req.Records)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": map[string]interface{}{}})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &Client{
		appID: "cli", appSecret: "sec", baseURL: srv.URL + "/open-apis",
		bitableID: "basApp", httpClient: srv.Client(),
		maxRetries: 1, backoffBase: time.Millisecond,
	}
	ops := NewBitableOps(client, "basApp")
	snap := models.BoardStatsSnapshot{
		Date:      "2026-08-12",
		ScrapedAt: time.Now(),
		Games: []models.GameBoardStats{
			{GameName: "鸣潮", Metrics: []models.BoardMetric{{Title: "咨询量", Value: "10"}}},
			{GameName: "原神", Metrics: []models.BoardMetric{{Title: "咨询量", Value: "20"}}},
		},
	}
	newC, updC, err := ops.SyncBoardStats(context.Background(), "tblBoard", snap)
	if err != nil {
		t.Fatalf("SyncBoardStats: %v", err)
	}
	if newC != 2 || updC != 0 {
		t.Fatalf("new=%d upd=%d", newC, updC)
	}
	if len(createdFields) != len(boardFieldDefs) {
		t.Fatalf("created fields=%d want %d: %v", len(createdFields), len(boardFieldDefs), createdFields)
	}
	if createdN != 2 {
		t.Fatalf("created records=%d", createdN)
	}
}

func TestShouldSyncBoardGame(t *testing.T) {
	if shouldSyncBoardGame(models.GameBoardStats{GameName: "鸣潮", Error: "API错误"}) {
		t.Fatal("error game should skip")
	}
	if shouldSyncBoardGame(models.GameBoardStats{GameName: "鸣潮"}) {
		t.Fatal("empty metrics should skip")
	}
	if shouldSyncBoardGame(models.GameBoardStats{
		GameName: "鸣潮",
		Metrics:  []models.BoardMetric{{Title: "未知字段", Value: "1"}},
	}) {
		t.Fatal("unmapped metrics should skip")
	}
	if !shouldSyncBoardGame(models.GameBoardStats{
		GameName: "原神",
		Metrics:  []models.BoardMetric{{Title: "咨询量", Value: "1"}},
	}) {
		t.Fatal("ok game should sync")
	}
}

func TestSyncBoardStatsSkipsFailedGames(t *testing.T) {
	var createdN int
	var deleted []string
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "tenant_access_token": "t-test", "expire": 7200,
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/fields", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0, "data": map[string]interface{}{"items": []any{}, "has_more": false},
			})
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
		}
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records", func(w http.ResponseWriter, r *http.Request) {
		dayMS := time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local).UnixMilli()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{
				"items": []map[string]interface{}{
					{
						"record_id": "rec_wuthering",
						"fields": map[string]interface{}{
							"日期": float64(dayMS), "账号": "18379745584", "游戏名称": "鸣潮",
						},
					},
					{
						"record_id": "rec_naruto",
						"fields": map[string]interface{}{
							"日期": float64(dayMS), "账号": "18379745584", "游戏名称": "火影忍者",
						},
					},
				},
				"has_more": false,
			},
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/batch_create", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Records []any `json:"records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		createdN += len(req.Records)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": map[string]interface{}{}})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
			return
		}
		if r.Method == http.MethodPut {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &Client{
		appID: "cli", appSecret: "sec", baseURL: srv.URL + "/open-apis",
		bitableID: "basApp", httpClient: srv.Client(),
		maxRetries: 1, backoffBase: time.Millisecond,
	}
	ops := NewBitableOps(client, "basApp")
	snap := models.BoardStatsSnapshot{
		Date: "2026-08-14", Account: "18379745584", ScrapedAt: time.Now(),
		Games: []models.GameBoardStats{
			{GameName: "火影忍者", Metrics: []models.BoardMetric{{Title: "咨询量", Value: "20"}}},
			{GameName: "鸣潮", Error: "API错误: FAIL_BIZ_NO_PRIVILEGE"},
			{GameName: "绝区零"}, // 无指标
		},
	}
	newC, updC, err := ops.SyncBoardStats(context.Background(), "tblBoard", snap)
	if err != nil {
		t.Fatal(err)
	}
	if newC != 0 || updC != 1 {
		t.Fatalf("new=%d upd=%d created=%d", newC, updC, createdN)
	}
	if len(deleted) != 0 {
		t.Fatalf("must not delete existing rows, deleted=%v", deleted)
	}
}

func TestSyncBoardStatsSameMonthUpdatesAndRefreshesDate(t *testing.T) {
	var putFields map[string]interface{}
	var putCalls, createCalls int
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local).UnixMilli()

	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "tenant_access_token": "t-test", "expire": 7200,
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/fields", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "data": map[string]interface{}{"items": []any{}, "has_more": false},
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{
				"items": []map[string]interface{}{{
					"record_id": "rec_aug",
					"fields": map[string]interface{}{
						"日期": float64(aug1), "账号": "13800000001", "游戏名称": "原神", "咨询量": 1.0,
					},
				}},
				"has_more": false,
			},
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/batch_create", func(w http.ResponseWriter, r *http.Request) {
		createCalls++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": map[string]interface{}{}})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCalls++
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			putFields, _ = body["fields"].(map[string]interface{})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &Client{
		appID: "cli", appSecret: "sec", baseURL: srv.URL + "/open-apis",
		bitableID: "basApp", httpClient: srv.Client(),
		maxRetries: 1, backoffBase: time.Millisecond,
	}
	ops := NewBitableOps(client, "basApp")
	snap := models.BoardStatsSnapshot{
		Date: "2026-08-15", Account: "13800000001", ScrapedAt: time.Now(),
		Games: []models.GameBoardStats{
			{GameName: "原神", Metrics: []models.BoardMetric{{Title: "咨询量", Value: "99"}}},
		},
	}
	newC, updC, err := ops.SyncBoardStats(context.Background(), "tblBoard", snap)
	if err != nil {
		t.Fatal(err)
	}
	if newC != 0 || updC != 1 || createCalls != 0 || putCalls != 1 {
		t.Fatalf("new=%d upd=%d create=%d put=%d", newC, updC, createCalls, putCalls)
	}
	wantMS := float64(time.Date(2026, 8, 15, 0, 0, 0, 0, time.Local).UnixMilli())
	if putFields["日期"] != wantMS {
		t.Fatalf("日期 should refresh to scrape day, got %v want %v", putFields["日期"], wantMS)
	}
}

func TestBoardMonthRecordKey(t *testing.T) {
	a := boardMonthRecordKey("2026-08-01", "u", "原神")
	b := boardMonthRecordKey("2026-08-15", "u", "原神")
	c := boardMonthRecordKey("2026-09-01", "u", "原神")
	if a != b {
		t.Fatalf("same month should match: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("different month should not match")
	}
}

func TestUpsertBoardManualRowCreateAndOverwrite(t *testing.T) {
	var putBody map[string]interface{}
	var createBody map[string]interface{}
	var putCalls, createCalls int
	dayMS := time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local).UnixMilli()

	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "tenant_access_token": "t-test", "expire": 7200,
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/fields", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "data": map[string]interface{}{"items": []any{}, "has_more": false},
		})
	})
	existing := []map[string]interface{}{}
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{"items": existing, "has_more": false},
		})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/batch_create", func(w http.ResponseWriter, r *http.Request) {
		createCalls++
		_ = json.NewDecoder(r.Body).Decode(&createBody)
		existing = []map[string]interface{}{
			{
				"record_id": "rec1",
				"fields": map[string]interface{}{
					"日期": float64(dayMS), "账号": "13800000001", "游戏名称": "原神", "咨询量": 10.0,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": map[string]interface{}{}})
	})
	mux.HandleFunc("/open-apis/bitable/v1/apps/basApp/tables/tblBoard/records/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCalls++
			_ = json.NewDecoder(r.Body).Decode(&putBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &Client{
		appID: "cli", appSecret: "sec", baseURL: srv.URL + "/open-apis",
		bitableID: "basApp", httpClient: srv.Client(),
		maxRetries: 1, backoffBase: time.Millisecond,
	}
	ops := NewBitableOps(client, "basApp")

	consult := 10.0
	action, err := ops.UpsertBoardManualRow(context.Background(), "tblBoard",
		"2026-08-14", "13800000001", "店A", "uid1", "原神",
		map[string]*float64{"咨询量": &consult}, time.Now())
	if err != nil || action != "created" {
		t.Fatalf("create action=%s err=%v", action, err)
	}
	if createCalls != 1 {
		t.Fatalf("createCalls=%d", createCalls)
	}

	action, err = ops.UpsertBoardManualRow(context.Background(), "tblBoard",
		"2026-08-14", "13800000001", "店A", "uid1", "原神",
		map[string]*float64{}, time.Now())
	if err != nil || action != "updated" {
		t.Fatalf("update action=%s err=%v", action, err)
	}
	if putCalls != 1 {
		t.Fatalf("putCalls=%d", putCalls)
	}
	fields, _ := putBody["fields"].(map[string]interface{})
	if fields == nil {
		t.Fatal("missing fields in put")
	}
	if v, ok := fields["咨询量"]; !ok || v != nil {
		t.Fatalf("咨询量 should be cleared (null), got %v ok=%v", v, ok)
	}
}

func TestManualBoardFieldsClearsUnsetMetrics(t *testing.T) {
	n := 3.0
	fields := manualBoardFields("2026-08-14", "a", "", "", "原神", map[string]*float64{
		"咨询量": &n,
	}, time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local))
	if fields["咨询量"] != 3.0 {
		t.Fatalf("consult=%v", fields["咨询量"])
	}
	if fields["发起报价量"] != nil {
		t.Fatalf("quote should be nil, got %v", fields["发起报价量"])
	}
	if fields["错误信息"] != nil {
		t.Fatalf("error should be nil")
	}
}
