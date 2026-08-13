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
