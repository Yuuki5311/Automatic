package accounts

import (
	"os"
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

func TestPathFromCookie(t *testing.T) {
	got := PathFromCookie("./data/cookies.json")
	want := filepath.Join(filepath.Dir("./data/cookies.json"), "accounts.json")
	if got != want {
		t.Fatalf("PathFromCookie(cookie)=%q, want %q", got, want)
	}
	empty := PathFromCookie("  ")
	if empty != filepath.Join("data", "accounts.json") {
		t.Fatalf("PathFromCookie(empty)=%q, want data/accounts.json", empty)
	}
}

func TestLoadCorruptJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if err := s.Load(); err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
	if len(s.List()) != 0 {
		t.Fatalf("corrupt load must not populate accounts: %+v", s.List())
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "missing.json"))
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("missing file should be empty, got %+v", s.List())
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

func TestFindByExternalID(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	a, _, err := s.UpsertFromRemoteKey("13800000009", "pw", "店", 99, 1, "cat")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.FindByExternalID(99)
	if !ok || got.ID != a.ID {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
	if _, ok := s.FindByExternalID(0); ok {
		t.Fatal("external 0 should miss")
	}
	if _, ok := s.FindByExternalID(1); ok {
		t.Fatal("unknown id should miss")
	}
}

func TestAccountKeyFromRemoteFallback(t *testing.T) {
	if got := AccountPhoneFromRemote("13800138000", "13900139000"); got != "13900139000" {
		t.Fatalf("third prefer: %q", got)
	}
	if got := AccountPhoneFromRemote("13800138000", "not-a-phone"); got != "13800138000" {
		t.Fatalf("mobile fallback: %q", got)
	}
	if got := AccountPhoneFromRemote("", "AABBCCDDEEFF00112233445566778899"); got != "" {
		t.Fatalf("reject cipher: %q", got)
	}
	if got := AccountKeyFromRemote("", "", 54); got != "" {
		t.Fatalf("reject shop id: %q", got)
	}
}

func TestUpsertFromRemoteRejectsNonMobile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	if _, _, err := s.UpsertFromRemoteKey("shop_54", "pw", "店A", 54, 1, "cat"); err == nil {
		t.Fatal("expected error for non-mobile")
	}
}

func TestUpsertFromRemoteDedupsSamePhone(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	a, created, err := s.UpsertFromRemoteKey("13800000001", "pw", "熊熊店", 59, 1, "cat")
	if err != nil || !created {
		t.Fatalf("a=%+v created=%v err=%v", a, created, err)
	}
	b, created2, err := s.UpsertFromRemoteKey("13800000001", "pw2", "熊熊店", 54, 1, "cat")
	if err != nil || created2 || b.ID != a.ID || b.ExternalID != 54 {
		t.Fatalf("b=%+v created2=%v err=%v", b, created2, err)
	}
	if n := len(s.List()); n != 1 {
		t.Fatalf("want 1 after phone dedup, got %d", n)
	}
}

func TestUpsertFromRemoteDedupsSameExternalID(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	_, _, err := s.UpsertFromRemoteKey("13800000001", "pw", "熊熊店", 59, 1, "cat")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.data.Accounts = append(s.data.Accounts, Account{
		ID: "dup", Username: "AABBCCDDEEFF00112233445566778899", Password: "pw", Enabled: true,
		ExternalID: 59, SupplierID: 1, ShopName: "熊熊店", PlatformKey: "cat",
	})
	s.mu.Unlock()

	got, created, err := s.UpsertFromRemoteKey("13800000001", "pw", "熊熊店", 59, 1, "cat")
	if err != nil || created {
		t.Fatalf("got=%+v created=%v err=%v", got, created, err)
	}
	if got.Username != "13800000001" || got.ExternalID != 59 {
		t.Fatalf("got=%+v", got)
	}
	if n := len(s.List()); n != 1 {
		t.Fatalf("want 1 after dedup, got %d %+v", n, s.List())
	}
}

func TestPruneInvalidPhones(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "a.json"))
	_ = s.Load()
	_, _, _ = s.UpsertFromRemoteKey("13800000001", "pw", "好", 1, 1, "cat")
	s.mu.Lock()
	s.data.Accounts = append(s.data.Accounts, Account{ID: "bad", Username: "shop_9", Enabled: true})
	s.mu.Unlock()
	n, err := s.PruneInvalidPhones()
	if err != nil || n != 1 || len(s.List()) != 1 {
		t.Fatalf("n=%d err=%v list=%+v", n, err, s.List())
	}
}
