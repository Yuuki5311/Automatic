package accounts

import (
	"path/filepath"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

func TestAddListDelete(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "accounts.json"))
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	a, err := s.Add("13800000000", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if a.Username != "13800000000" || a.Password != "secret" || a.ID == "" {
		t.Fatalf("bad account: %+v", a)
	}
	if !filepath.IsAbs(a.CookiePath) && a.CookiePath == "" {
		t.Fatal("cookie path empty")
	}
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("expected empty")
	}
}

func TestAddDuplicate(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	_ = s.Load()
	if _, err := s.Add("u1", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("u1", "p2"); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestMigrateFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	s := NewStore(path)
	_ = s.Load()
	ok, err := s.MigrateFromConfig(config.JYMConfig{
		Username: "legacy", Password: "pw", CookiePath: filepath.Join(dir, "cookies.json"),
	})
	if err != nil || !ok {
		t.Fatalf("migrate: ok=%v err=%v", ok, err)
	}
	ok2, err := s.MigrateFromConfig(config.JYMConfig{Username: "other", Password: "x"})
	if err != nil || ok2 {
		t.Fatalf("second migrate should no-op: ok=%v err=%v", ok2, err)
	}
	if len(s.List()) != 1 || s.List()[0].Username != "legacy" {
		t.Fatalf("%+v", s.List())
	}
}

func TestUpdateStatus(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	a, _ := s.Add("u", "p")
	if err := s.UpdateStatus(a.ID, "skipped", "login failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(a.ID)
	if got.LastStatus != "skipped" || got.LastError != "login failed" || got.LastRunAt.IsZero() {
		t.Fatalf("%+v", got)
	}
}
