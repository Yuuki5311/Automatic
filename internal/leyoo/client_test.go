package leyoo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCatBySupplierFiltersLocally(t *testing.T) {
	payload := map[string]interface{}{
		"code": 0,
		"msg":  "ok",
		"data": []map[string]interface{}{
			{"id": 1, "mobile": "13800000001", "platform_key": "cat", "supplier_id": 1, "cookie": "a=1"},
			{"id": 2, "mobile": "13800000002", "platform_key": "cat", "supplier_id": 2, "cookie": "a=2"},
			{"id": 3, "mobile": "13800000003", "platform_key": "dewu", "supplier_id": 1, "cookie": "a=3"},
			{"id": 4, "mobile": "", "platform_key": "cat", "supplier_id": 1, "cookie": "a=4", "name": "无手机号店"},
		},
	}
	body, _ := json.Marshal(payload)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.ListCatBySupplier(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 cat shops incl empty mobile, got=%+v", got)
	}
	if got[0].Mobile != "13800000001" || got[1].ID != 4 {
		t.Fatalf("got=%+v", got)
	}
}
