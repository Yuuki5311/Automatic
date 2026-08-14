package web

import (
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/status"
)

func TestGroupHistoryTodayYesterday(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 5, 0, 0, now.Location())
	yest := today.AddDate(0, 0, -1)
	older := today.AddDate(0, 0, -3)
	groups := groupHistory([]status.HistoryEntry{
		{Account: "a", At: today, Status: "ok"},
		{Account: "b", At: yest, Status: "skipped", Error: "x"},
		{Account: "c", At: older, Status: "ok"},
	})
	if len(groups) != 3 {
		t.Fatalf("groups=%d", len(groups))
	}
	if groups[0].Label != "今天 ("+today.Format("2006-01-02")+")" {
		t.Fatalf("g0=%q", groups[0].Label)
	}
	if groups[1].Label != "昨天 ("+yest.Format("2006-01-02")+")" {
		t.Fatalf("g1=%q", groups[1].Label)
	}
	if groups[2].Label != older.Format("2006-01-02") {
		t.Fatalf("g2=%q", groups[2].Label)
	}
}
