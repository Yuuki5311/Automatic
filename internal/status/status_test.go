package status

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	s.RunFinished(nil, 3, 1, 0)

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
	if snap.LastRun.OkAccounts != 1 || snap.LastRun.SkippedAccounts != 0 {
		t.Fatalf("account counts=%+v", snap.LastRun)
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

func TestRunFinishedPartialSkipStaysSuccess(t *testing.T) {
	s := NewStore()
	s.RunStarted()
	s.RunFinished(nil, 2, 1, 1)

	snap := s.Snapshot()
	if snap.LastRun == nil || !snap.LastRun.Success {
		t.Fatalf("partial skip should succeed: %+v", snap.LastRun)
	}
	if snap.LastRun.OkAccounts != 1 || snap.LastRun.SkippedAccounts != 1 {
		t.Fatalf("counts ok=%d skipped=%d", snap.LastRun.OkAccounts, snap.LastRun.SkippedAccounts)
	}
	if snap.LastRun.Error != "" {
		t.Fatalf("Error=%q, want empty", snap.LastRun.Error)
	}

	raw, err := json.Marshal(snap.LastRun)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["ok_accounts"] != float64(1) || m["skipped_accounts"] != float64(1) {
		t.Fatalf("json account counts: %s", raw)
	}
}

func TestRunFinishedAllSkippedStoresError(t *testing.T) {
	s := NewStore()
	s.RunStarted()
	s.RunFinished(errors.New("全部账户跳过"), 0, 0, 2)

	snap := s.Snapshot()
	if snap.LastRun == nil || snap.LastRun.Success {
		t.Fatalf("all skipped should fail: %+v", snap.LastRun)
	}
	if snap.LastRun.Error != "全部账户跳过" {
		t.Fatalf("Error=%q", snap.LastRun.Error)
	}
	if snap.LastRun.OkAccounts != 0 || snap.LastRun.SkippedAccounts != 2 {
		t.Fatalf("counts ok=%d skipped=%d", snap.LastRun.OkAccounts, snap.LastRun.SkippedAccounts)
	}
}

func TestSetAccountsAppearsInSnapshot(t *testing.T) {
	s := NewStore()
	runAt := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	s.SetAccounts([]AccountStatus{
		{
			ID:          "acc-1",
			Username:    "13800000000",
			Enabled:     true,
			LastStatus:  "ok",
			LastRunAt:   runAt,
			CookieValid: true,
			HasPassword: true,
		},
		{
			ID:          "acc-2",
			Username:    "13900000000",
			Enabled:     false,
			LastStatus:  "skipped",
			LastError:   "login failed",
			CookieValid: false,
			HasPassword: false,
		},
	})

	snap := s.Snapshot()
	if len(snap.Accounts) != 2 {
		t.Fatalf("accounts=%d, want 2", len(snap.Accounts))
	}
	if snap.Accounts[0].Username != "13800000000" || !snap.Accounts[0].CookieValid {
		t.Fatalf("account[0]: %+v", snap.Accounts[0])
	}
	if snap.Accounts[1].LastError != "login failed" {
		t.Fatalf("account[1] LastError=%q", snap.Accounts[1].LastError)
	}

	snap.Accounts[0].Username = "mutated"
	snap2 := s.Snapshot()
	if snap2.Accounts[0].Username != "13800000000" {
		t.Fatalf("Snapshot should deep-copy accounts, got Username=%q", snap2.Accounts[0].Username)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	accounts, ok := m["accounts"].([]any)
	if !ok || len(accounts) != 2 {
		t.Fatalf("json accounts=%v", m["accounts"])
	}
}

func TestSetAccountsNilClearsList(t *testing.T) {
	s := NewStore()
	s.SetAccounts([]AccountStatus{{ID: "acc-1", Username: "13800000000"}})
	s.SetAccounts(nil)

	snap := s.Snapshot()
	if snap.Accounts != nil {
		t.Fatalf("accounts=%v, want nil", snap.Accounts)
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
