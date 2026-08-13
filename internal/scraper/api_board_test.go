package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestParseRecycleStatsJSON_GenshinEightMetrics(t *testing.T) {
	body := []byte(`{
	  "api":"mtop.com.jym.merchant.board.recyclestats",
	  "ret":["SUCCESS::调用成功"],
	  "data":{"result":[
	    {"title":"咨询量","staData":"186","properties":{"tips":"向您发起回收咨询的数量"}},
	    {"title":"发起报价量","staData":"50","properties":{"tips":"x"}},
	    {"title":"回收成功订单数","staData":"19","properties":{"tips":"x"}},
	    {"title":"回收成功金额","staData":"9650.00","staUnit":"元","properties":{"tips":"x"}},
	    {"title":"回收成功率","staData":"10.22","staUnit":"%","properties":{"tips":"x"}},
	    {"title":"回收满意度","staData":"100.00","staUnit":"%","properties":{"tips":"x"}},
	    {"title":"回收主页咨询量","staData":"0","properties":{"tips":"x"}},
	    {"title":"回收主页成功订单数","staData":"0","properties":{"tips":"x"}}
	  ]},
	  "v":"1.0"
	}`)
	got, err := ParseRecycleStatsJSON("原神", 1009609, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Metrics) != 8 {
		t.Fatalf("metrics=%d", len(got.Metrics))
	}
	if got.Metrics[0].Value != "186" || got.Metrics[3].Unit != "元" {
		t.Fatalf("%+v", got.Metrics)
	}
	if got.GameName != "原神" || got.GameID != 1009609 || got.TimeKey != "yesterday" {
		t.Fatalf("metadata: %+v", got)
	}
	if got.Metrics[0].Title != "咨询量" || got.Metrics[0].Tips != "向您发起回收咨询的数量" {
		t.Fatalf("first metric: %+v", got.Metrics[0])
	}
	if got.RawJSON == "" || !strings.Contains(got.RawJSON, `"result"`) {
		t.Fatalf("RawJSON should contain data payload, got %q", got.RawJSON)
	}
}

func TestParseRecycleStatsJSON_FailRet(t *testing.T) {
	_, err := ParseRecycleStatsJSON("火影忍者", 1003132, []byte(`{"ret":["FAIL_SYS_SESSION_EXPIRED::x"],"data":{}}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errCookieExpired) {
		t.Fatalf("session FAIL should map to errCookieExpired, got %v", err)
	}
}

func TestParseRecycleStatsJSON_TestdataFile(t *testing.T) {
	body, err := os.ReadFile("testdata/recyclestats_genshin.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRecycleStatsJSON("原神", 1009609, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Metrics) != 8 {
		t.Fatalf("metrics=%d", len(got.Metrics))
	}
}

func TestBuildRecycleStatsURL_QueryParams(t *testing.T) {
	c := &apiClient{mtopToken: "testtoken"}
	u, err := c.buildRecycleStatsURL(gameIDGenshin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "valueType=original") {
		t.Fatalf("missing valueType=original in %s", u)
	}
	if !strings.Contains(u, "preventFallback=true") {
		t.Fatalf("missing preventFallback=true in %s", u)
	}
}

func TestFetchBoardStats_Success(t *testing.T) {
	const respBody = `{"ret":["SUCCESS::调用成功"],"data":{"result":[{"title":"咨询量","staData":"10","properties":{"tips":"t"}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("valueType") != "original" || r.URL.Query().Get("preventFallback") != "true" {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	c := &apiClient{
		httpClient: srv.Client(),
		cookies:    []models.CookieEntry{{Name: "_m_h5_tk", Value: "abc_123"}},
		mtopToken:  "abc",
	}
	origBase := mtopBaseURL
	mtopBaseURL = srv.URL
	t.Cleanup(func() { mtopBaseURL = origBase })

	got, err := c.FetchBoardStats(context.Background(), "原神")
	if err != nil {
		t.Fatal(err)
	}
	if got.GameName != "原神" || got.GameID != gameIDGenshin || len(got.Metrics) != 1 {
		t.Fatalf("%+v", got)
	}
	if got.Metrics[0].Value != "10" {
		t.Fatalf("metric: %+v", got.Metrics[0])
	}
}

func TestFetchBoardStats_UnknownGame(t *testing.T) {
	c := &apiClient{}
	_, err := c.FetchBoardStats(context.Background(), "未知游戏")
	if err == nil || !strings.Contains(err.Error(), "未找到游戏ID") {
		t.Fatalf("got err=%v", err)
	}
}
