package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDailyCronRoundTrip(t *testing.T) {
	expr, err := DailyCronExpr(9, 0)
	if err != nil || expr != "0 9 * * *" {
		t.Fatalf("expr=%q err=%v", expr, err)
	}
	h, m, ok := ParseDailyCron(expr)
	if !ok || h != 9 || m != 0 {
		t.Fatalf("parse %d:%d ok=%v", h, m, ok)
	}
	if HHMMFromCron(expr) != "09:00" {
		t.Fatal(HHMMFromCron(expr))
	}
	if CronLabelFromExpr(expr) != "每天 09:00" {
		t.Fatal(CronLabelFromExpr(expr))
	}
}

func TestParseHHMM(t *testing.T) {
	h, m, err := ParseHHMM("14:30")
	if err != nil || h != 14 || m != 30 {
		t.Fatalf("%d:%d %v", h, m, err)
	}
	if _, _, err := ParseHHMM("25:00"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSaveCronExpr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "scraper:\n  mode: api\n  cron_expr: \"0 9 * * *\"\n  stats_dir: ./data/stats\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveCronExpr(path, "30 14 * * *"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `cron_expr: "30 14 * * *"`) {
		t.Fatalf("got=%s", got)
	}
	if !strings.Contains(string(got), "mode: api") {
		t.Fatal("lost other fields")
	}
}
