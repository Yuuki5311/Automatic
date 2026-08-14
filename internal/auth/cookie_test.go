package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// ---------- LoadCookies ----------

func TestLoadCookies_FileNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	data, err := LoadCookies(path)
	if err != nil {
		t.Fatalf("LoadCookies(%q) returned error for missing file: %v", path, err)
	}
	if data == nil {
		t.Fatal("LoadCookies returned nil data for missing file, want empty CookieData")
	}
	if len(data.Cookies) != 0 {
		t.Fatalf("LoadCookies returned %d cookies for missing file, want 0", len(data.Cookies))
	}
}

func TestLoadCookies_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	if _, err := LoadCookies(path); err == nil {
		t.Fatal("LoadCookies accepted invalid JSON, want error")
	}
}

func TestLoadCookies_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	want := &models.CookieData{
		Cookies: []models.CookieEntry{
			{Name: "token", Value: "abc", Domain: ".jiaoyimao.com", Path: "/"},
		},
		ExpiresAt: time.Now().Add(24 * time.Hour).Truncate(time.Second),
	}
	if err := SaveCookies(path, want); err != nil {
		t.Fatalf("SaveCookies failed: %v", err)
	}

	got, err := LoadCookies(path)
	if err != nil {
		t.Fatalf("LoadCookies failed: %v", err)
	}
	if len(got.Cookies) != 1 || got.Cookies[0].Name != "token" || got.Cookies[0].Value != "abc" {
		t.Fatalf("LoadCookies round-trip mismatch, got %+v", got.Cookies)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("ExpiresAt mismatch: got %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt mismatch: got %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
}

// ---------- SaveCookies ----------

func TestSaveCookies_SetsUpdatedAtAndContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	before := time.Now()
	data := &models.CookieData{
		Cookies: []models.CookieEntry{{Name: "token", Value: "v1"}},
	}
	if err := SaveCookies(path, data); err != nil {
		t.Fatalf("SaveCookies failed: %v", err)
	}
	if data.UpdatedAt.Before(before) {
		t.Fatalf("UpdatedAt not set to current time: %v", data.UpdatedAt)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("saved file not readable: %v", err)
	}
	var back models.CookieData
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
	if len(back.Cookies) != 1 || back.Cookies[0].Name != "token" {
		t.Fatalf("saved content mismatch: %+v", back.Cookies)
	}
}

func TestSaveCookies_CreatesNestedDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "cookies.json")
	data := &models.CookieData{}
	if err := SaveCookies(path, data); err != nil {
		t.Fatalf("SaveCookies with nested dirs failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file missing after SaveCookies: %v", err)
	}
}

func TestSaveCookies_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "cookies.json")
	if err := SaveCookies(path, &models.CookieData{}); err != nil {
		t.Fatalf("SaveCookies failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("cookies file permission = %o, want 600", perm)
	}
}

// ---------- IsCookieValid ----------

func TestIsCookieValid_Nil(t *testing.T) {
	if IsCookieValid(nil) {
		t.Fatal("IsCookieValid(nil) = true, want false")
	}
}

func TestIsCookieValid_EmptyCookies(t *testing.T) {
	if IsCookieValid(&models.CookieData{}) {
		t.Fatal("IsCookieValid(empty) = true, want false")
	}
}

func TestIsCookieValid_Expired(t *testing.T) {
	data := &models.CookieData{
		Cookies:   []models.CookieEntry{{Name: "token", Value: "v"}},
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	if IsCookieValid(data) {
		t.Fatal("IsCookieValid(expired) = true, want false")
	}
}

func TestIsCookieValid_NoSessionCookie(t *testing.T) {
	data := &models.CookieData{
		Cookies: []models.CookieEntry{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: ""},
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if IsCookieValid(data) {
		t.Fatal("IsCookieValid(no session cookie) = true, want false")
	}
}

func TestIsCookieValid_EmptySessionValue(t *testing.T) {
	data := &models.CookieData{
		Cookies: []models.CookieEntry{
			{Name: "token", Value: ""},
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if IsCookieValid(data) {
		t.Fatal("IsCookieValid(token with empty value) = true, want false")
	}
}

func TestIsCookieValid_Valid(t *testing.T) {
	for _, name := range []string{"token", "SESSION", "jym_token"} {
		data := &models.CookieData{
			Cookies:   []models.CookieEntry{{Name: name, Value: "secret"}},
			ExpiresAt: time.Now().Add(time.Hour),
		}
		if !IsCookieValid(data) {
			t.Fatalf("IsCookieValid(session cookie %q) = false, want true", name)
		}
	}
}

func TestMemberUID(t *testing.T) {
	if got := MemberUID(nil); got != "" {
		t.Fatalf("nil => %q", got)
	}
	data := &models.CookieData{Cookies: []models.CookieEntry{
		{Name: "token", Value: "x"},
		{Name: "ieu_member_uid", Value: "1727867117436205"},
	}}
	if got := MemberUID(data); got != "1727867117436205" {
		t.Fatalf("got %q", got)
	}
}

// ---------- ImportFromJSON ----------

const editThisCookieSample = `[
  {
    "name": "token",
    "value": "eyJhbGciOiJIUzI1NiJ9",
    "domain": ".jiaoyimao.com",
    "path": "/",
    "expirationDate": 1900000000,
    "httpOnly": true,
    "secure": true
  },
  {
    "name": "sessionid",
    "value": "abc123",
    "domain": "merchant.jiaoyimao.com",
    "path": "/",
    "expirationDate": 1800000000,
    "httpOnly": false,
    "secure": false
  }
]`

func TestImportFromJSON_EditThisCookieFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imported-cookies.json")
	if err := ImportFromJSON(path, []byte(editThisCookieSample)); err != nil {
		t.Fatalf("ImportFromJSON failed: %v", err)
	}

	// 文件应已被 SaveCookies 落盘
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("imported cookie file missing: %v", err)
	}
	var data models.CookieData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("imported file not valid JSON: %v", err)
	}

	if len(data.Cookies) != 2 {
		t.Fatalf("imported %d cookies, want 2", len(data.Cookies))
	}
	first := data.Cookies[0]
	if first.Name != "token" || first.Value != "eyJhbGciOiJIUzI1NiJ9" ||
		first.Domain != ".jiaoyimao.com" || first.Path != "/" ||
		first.Expires != 1900000000 || !first.HTTPOnly || !first.Secure {
		t.Fatalf("first imported cookie mismatch: %+v", first)
	}

	// ExpiresAt 取最大 expirationDate
	wantExpiry := time.Unix(1900000000, 0)
	if !data.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", data.ExpiresAt, wantExpiry)
	}
}

func TestImportFromJSON_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-import.json")
	if err := ImportFromJSON(path, []byte("{not json")); err == nil {
		t.Fatal("ImportFromJSON accepted invalid JSON, want error")
	}
}

func TestImportFromJSON_EmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-import.json")
	if err := ImportFromJSON(path, []byte("[]")); err != nil {
		t.Fatalf("ImportFromJSON([]) failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("imported file missing: %v", err)
	}
	var data models.CookieData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("imported file not valid JSON: %v", err)
	}
	if len(data.Cookies) != 0 {
		t.Fatalf("imported %d cookies, want 0", len(data.Cookies))
	}
	if !data.ExpiresAt.Equal(time.Unix(0, 0)) {
		t.Fatalf("ExpiresAt = %v, want Unix(0,0)", data.ExpiresAt)
	}
	// 空导入后的数据应被视为无效
	if IsCookieValid(&data) {
		t.Fatal("IsCookieValid(empty import) = true, want false")
	}
}
