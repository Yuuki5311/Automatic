package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRecycleOrderJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
	order := RecycleOrder{
		OrderID:      "JM20260812103000",
		GameName:     "火影忍者",
		ServerRegion: "安卓-官服",
		AccountInfo:  "V6账号，忍者齐全",
		Price:        1250.5,
		Status:       "已完成",
		CreateTime:   now,
		CompleteTime: now.Add(2 * time.Hour),
		BuyerInfo:    "买家A",
		Remarks:      "无",
		RawData:      `{"extra":"field"}`,
	}

	data, err := json.Marshal(order)
	if err != nil {
		t.Fatalf("json.Marshal(RecycleOrder) error: %v", err)
	}

	var got RecycleOrder
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(RecycleOrder) error: %v", err)
	}

	if got != order {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, order)
	}

	// Verify the JSON key naming convention (snake_case).
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal raw error: %v", err)
	}
	for _, key := range []string{"order_id", "game_name", "server_region", "account_info", "price", "status", "create_time", "complete_time", "buyer_info", "remarks", "raw_data"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("RecycleOrder JSON missing key %q", key)
		}
	}
}

func TestCookieDataJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	data := CookieData{
		Cookies: []CookieEntry{
			{
				Name:     "session_id",
				Value:    "abc123",
				Domain:   ".jiaoyimao.com",
				Path:     "/",
				Expires:  1780000000.5,
				HTTPOnly: true,
				Secure:   true,
			},
		},
		UpdatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal(CookieData) error: %v", err)
	}

	var got CookieData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(CookieData) error: %v", err)
	}

	if len(got.Cookies) != 1 {
		t.Fatalf("Cookies length = %d, want 1", len(got.Cookies))
	}
	c := got.Cookies[0]
	if c.Name != "session_id" || c.Value != "abc123" || c.Domain != ".jiaoyimao.com" || c.Path != "/" {
		t.Errorf("CookieEntry mismatch: %+v", c)
	}
	if c.Expires != 1780000000.5 {
		t.Errorf("CookieEntry.Expires = %v, want 1780000000.5", c.Expires)
	}
	if !c.HTTPOnly || !c.Secure {
		t.Errorf("CookieEntry HTTPOnly/Secure mismatch: %+v", c)
	}
	if !got.UpdatedAt.Equal(now) || !got.ExpiresAt.Equal(data.ExpiresAt) {
		t.Errorf("timestamps mismatch: updated=%v want=%v, expires=%v want=%v", got.UpdatedAt, now, got.ExpiresAt, data.ExpiresAt)
	}
}

func TestFeishuRecordJSON(t *testing.T) {
	rec := FeishuRecord{
		RecordID: "rec123",
		Fields:   map[string]interface{}{"金额": 1250.5, "状态": "已完成"},
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal(FeishuRecord) error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal(FeishuRecord) error: %v", err)
	}
	if raw["record_id"] != "rec123" {
		t.Errorf("raw[record_id] = %v, want %q", raw["record_id"], "rec123")
	}
	if _, ok := raw["fields"]; !ok {
		t.Error("raw[fields] missing")
	}
}
