package status

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestRecordGameStatsStoresMetricsAndCount(t *testing.T) {
	s := NewStore()
	metrics := []models.BoardMetric{
		{Title: "咨询量", Value: "186"},
		{Title: "回收成功金额", Value: "9650.00", Unit: "元"},
	}
	s.RecordGameStats("原神", metrics, nil)

	snap := s.Snapshot()
	if len(snap.Games) != 1 {
		t.Fatalf("games=%d, want 1", len(snap.Games))
	}
	g := snap.Games[0]
	if g.GameName != "原神" || g.TableKey != "原神" {
		t.Fatalf("game identity: %+v", g)
	}
	if !g.Success {
		t.Fatal("expected success")
	}
	if g.RecordCount != 2 {
		t.Fatalf("RecordCount=%d, want 2", g.RecordCount)
	}
	if len(g.Metrics) != 2 || g.Metrics[0].Title != "咨询量" || g.Metrics[1].Unit != "元" {
		t.Fatalf("metrics: %+v", g.Metrics)
	}
}

func TestRecordGameStatsFailureSetsError(t *testing.T) {
	s := NewStore()
	s.RecordGameStats("鸣潮", nil, errors.New("mtop boom"))

	g := s.Snapshot().Games[0]
	if g.Success {
		t.Fatal("expected failure")
	}
	if g.Error != "mtop boom" {
		t.Fatalf("Error=%q", g.Error)
	}
	if g.RecordCount != 0 {
		t.Fatalf("RecordCount=%d, want 0", g.RecordCount)
	}
}

func TestRecordGameStatsUpsertsByGameName(t *testing.T) {
	s := NewStore()
	s.RecordGameStats("原神", []models.BoardMetric{{Title: "咨询量", Value: "1"}}, nil)
	s.RecordGameStats("原神", []models.BoardMetric{{Title: "咨询量", Value: "2"}}, nil)

	snap := s.Snapshot()
	if len(snap.Games) != 1 {
		t.Fatalf("games=%d, want 1 after upsert", len(snap.Games))
	}
	if snap.Games[0].Metrics[0].Value != "2" {
		t.Fatalf("upsert did not replace metrics: %+v", snap.Games[0].Metrics)
	}
}

func TestRunFinishedPreservesStatsDateAndSuccessCount(t *testing.T) {
	s := NewStore()
	s.RunStarted()
	s.SetStatsDate("2026-08-12")
	s.RunFinished(nil, 3)

	snap := s.Snapshot()
	if snap.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	if snap.LastRun.StatsDate != "2026-08-12" {
		t.Fatalf("StatsDate=%q", snap.LastRun.StatsDate)
	}
	if snap.LastRun.TotalOrders != 3 {
		t.Fatalf("TotalOrders=%d, want 3 (success games)", snap.LastRun.TotalOrders)
	}
	if !snap.LastRun.Success {
		t.Fatal("expected success")
	}

	raw, err := json.Marshal(snap.LastRun)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["stats_date"] != "2026-08-12" {
		t.Fatalf("json stats_date=%v", m["stats_date"])
	}
}

func TestGameResultMetricsJSONTag(t *testing.T) {
	s := NewStore()
	s.RecordGameStats("原神", []models.BoardMetric{{Title: "咨询量", Value: "1"}}, nil)
	raw, err := json.Marshal(s.Snapshot().Games[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["metrics"]; !ok {
		t.Fatalf("missing metrics key: %s", raw)
	}
}
