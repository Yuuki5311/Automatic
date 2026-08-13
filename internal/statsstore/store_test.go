package statsstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestSaveOverwriteSameDate(t *testing.T) {
	dir := t.TempDir()
	snap1 := models.BoardStatsSnapshot{
		Date: "2026-08-12", ScrapedAt: time.Now(),
		Games: []models.GameBoardStats{{GameName: "鸣潮", GameID: 2007615, TimeKey: "yesterday",
			Metrics: []models.BoardMetric{{Title: "咨询量", Value: "1"}}}},
	}
	snap2 := snap1
	snap2.Games[0].Metrics[0].Value = "229"
	if _, err := Save(dir, snap1); err != nil {
		t.Fatal(err)
	}
	path, err := Save(dir, snap2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	if got.Games[0].Metrics[0].Value != "229" {
		t.Fatalf("want overwrite 229, got %s", got.Games[0].Metrics[0].Value)
	}
	if filepath.Base(path) != "2026-08-12.json" {
		t.Fatalf("path=%s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
