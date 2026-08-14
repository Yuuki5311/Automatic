package scrapehistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendListNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scrape_history.json")
	s := NewStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("a1", "ok", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.Append("a2", "skipped", "cookie expired"); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].Account != "a2" || list[1].Account != "a1" {
		t.Fatalf("order=%+v", list)
	}
	if list[0].Status != "skipped" || list[0].Error != "cookie expired" {
		t.Fatalf("entry0=%+v", list[0])
	}

	s2 := NewStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) != 2 {
		t.Fatal("persist failed")
	}
}

func TestPruneOlderThanRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scrape_history.json")
	old := Entry{ID: "old", At: time.Now().Add(-Retention - time.Hour), Account: "old", Status: "ok"}
	raw, _ := json.Marshal(fileData{Entries: []Entry{old}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("want pruned empty, got %+v", s.List())
	}
	if err := s.Append("new", "ok", ""); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 || s.List()[0].Account != "new" {
		t.Fatalf("got %+v", s.List())
	}
}

func TestCorruptJSONBecomesEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scrape_history.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("want empty, got %+v", s.List())
	}
}

func TestPathFromAccounts(t *testing.T) {
	got := PathFromAccounts(`C:\app\data\accounts.json`)
	if filepath.Base(got) != "scrape_history.json" {
		t.Fatalf("got %q", got)
	}
	if filepath.Base(filepath.Dir(got)) != "data" {
		t.Fatalf("dir=%q", filepath.Dir(got))
	}
}

func TestTruncateError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.json")
	s := NewStore(path)
	_ = s.Load()
	runes := make([]rune, 250)
	for i := range runes {
		runes[i] = 'x'
	}
	if err := s.Append("u", "skipped", string(runes)); err != nil {
		t.Fatal(err)
	}
	if got := []rune(s.List()[0].Error); len(got) != maxErrorRunes {
		t.Fatalf("len=%d", len(got))
	}
}
