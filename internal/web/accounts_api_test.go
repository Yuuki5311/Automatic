package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/leyoo"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

type accountsAPIEnv struct {
	ts   *httptest.Server
	acct *accounts.Store
	srv  *Server
}

func newAccountsAPI(t *testing.T) accountsAPIEnv {
	t.Helper()
	acct := accounts.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err := acct.Load(); err != nil {
		t.Fatal(err)
	}
	s, err := New(status.NewStore(), &config.Config{}, nil, nil, nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)
	return accountsAPIEnv{ts: ts, acct: acct, srv: s}
}

func TestAccountsListHidesPassword(t *testing.T) {
	env := newAccountsAPI(t)
	a, err := env.acct.Add("13800000000", "s3cret")
	if err != nil {
		t.Fatal(err)
	}

	listResp, err := http.Get(env.ts.URL + "/api/accounts")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	listBody, _ := io.ReadAll(listResp.Body)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", listResp.StatusCode, listBody)
	}
	if strings.Contains(string(listBody), "s3cret") || strings.Contains(string(listBody), `"password"`) {
		t.Fatalf("list leaked password: %s", listBody)
	}

	var list []status.AccountStatus
	if err := json.Unmarshal(listBody, &list); err != nil {
		t.Fatalf("list JSON: %v body=%s", err, listBody)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d body=%s", len(list), listBody)
	}
	got := list[0]
	if got.ID != a.ID || got.Username != "13800000000" || !got.Enabled || !got.HasPassword {
		t.Fatalf("item=%+v", got)
	}
	if got.CookieValid {
		t.Fatal("cookie_valid should be false before import")
	}
}

func TestAccountsManualAddRemoved(t *testing.T) {
	env := newAccountsAPI(t)
	resp, err := http.Post(env.ts.URL+"/api/accounts", "application/json",
		strings.NewReader(`{"username":"13800000000","password":"s3cret"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("manual add should be removed, got %d", resp.StatusCode)
	}
}

func TestAccountsDelete(t *testing.T) {
	env := newAccountsAPI(t)
	id := seedAccount(t, env, "u1", "p1")

	req, _ := http.NewRequest(http.MethodDelete, env.ts.URL+"/api/accounts/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status=%d", resp.StatusCode)
	}

	list := getAccounts(t, env.ts)
	if len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestAccountsImportCookiesSetsCookieValid(t *testing.T) {
	env := newAccountsAPI(t)
	id := seedAccount(t, env, "cookieu", "pw")

	const cookiesJSON = `[{"name":"token","value":"abc","domain":".jiaoyimao.com","path":"/","expirationDate":1900000000,"httpOnly":true,"secure":true}]`
	resp, err := http.Post(env.ts.URL+"/api/accounts/"+id+"/cookies", "application/json", strings.NewReader(cookiesJSON))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status=%d body=%s", resp.StatusCode, body)
	}

	list := getAccounts(t, env.ts)
	if len(list) != 1 || !list[0].CookieValid {
		t.Fatalf("cookie_valid not set: %+v", list)
	}
}

func TestAccountLoginUnknownID(t *testing.T) {
	env := newAccountsAPI(t)
	resp, err := http.Post(env.ts.URL+"/api/accounts/no-such-id/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

func TestAccountLoginStarts(t *testing.T) {
	env := newAccountsAPI(t)
	id := seedAccount(t, env, "loginuser", "pw")

	resp, err := http.Post(env.ts.URL+"/api/accounts/"+id+"/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "started" {
		t.Fatalf("body=%v", body)
	}
}

func TestLegacyLoginRequiresExactlyOneAccount(t *testing.T) {
	env := newAccountsAPI(t)

	resp, err := http.Post(env.ts.URL+"/api/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("0 accounts: status=%d body=%s", resp.StatusCode, body)
	}

	seedAccount(t, env, "a1", "p")
	seedAccount(t, env, "a2", "p")
	resp, err = http.Post(env.ts.URL+"/api/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("2 accounts: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestLegacyLoginSingleAccountStarts(t *testing.T) {
	env := newAccountsAPI(t)
	seedAccount(t, env, "only", "pw")

	resp, err := http.Post(env.ts.URL+"/api/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "started" {
		t.Fatalf("body=%v", body)
	}
}

func TestIndexRefreshesAccountsFromStore(t *testing.T) {
	acct := accounts.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err := acct.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := acct.Add("indexu", "pw"); err != nil {
		t.Fatal(err)
	}
	s, err := New(status.NewStore(), &config.Config{}, nil, nil, nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "indexu") {
		t.Fatalf("index should render accounts from store, body=%s", rec.Body.String())
	}
}

func TestAccountsAppearInStatusSnapshot(t *testing.T) {
	env := newAccountsAPI(t)
	seedAccount(t, env, "snapu", "pw")

	resp, err := http.Get(env.ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap status.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Accounts) != 1 {
		t.Fatalf("status accounts=%+v", snap.Accounts)
	}
	if snap.Accounts[0].Username != "snapu" || !snap.Accounts[0].HasPassword {
		t.Fatalf("status account=%+v", snap.Accounts[0])
	}
}

func TestAccountsEnableDisable(t *testing.T) {
	env := newAccountsAPI(t)
	id := seedAccount(t, env, "toggle", "pw")

	resp, err := http.Post(env.ts.URL+"/api/accounts/"+id+"/disable", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable status=%d", resp.StatusCode)
	}
	list := getAccounts(t, env.ts)
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("want disabled: %+v", list)
	}

	resp, err = http.Post(env.ts.URL+"/api/accounts/"+id+"/enable", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	list = getAccounts(t, env.ts)
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("want enabled: %+v", list)
	}
}

func TestAccountsPullImportsCatCookies(t *testing.T) {
	env := newAccountsAPI(t)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":0,"msg":"ok","data":[
			{"id":11,"name":"猫店","cookie":"ieu_member_uid=1; ieu_member_token=tok","mobile":"13900000001","platform_key":"cat","supplier_id":1,"third_password":"pw1"},
			{"id":12,"name":"别的平台","cookie":"x=1","mobile":"13900000002","platform_key":"other","supplier_id":1,"third_password":"pw2"},
			{"id":13,"name":"别的供应商","cookie":"ieu_member_uid=2","mobile":"13900000003","platform_key":"cat","supplier_id":2,"third_password":"pw3"}
		]}`)
	}))
	t.Cleanup(mock.Close)
	env.srv.leyoo = leyoo.NewClient(mock.URL)

	resp, err := http.Post(env.ts.URL+"/api/accounts/pull", "application/json",
		strings.NewReader(`{"supplier_id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("pull status=%d body=%s", resp.StatusCode, b)
	}

	deadline := time.Now().Add(3 * time.Second)
	var list []status.AccountStatus
	for time.Now().Before(deadline) {
		list = getAccounts(t, env.ts)
		if len(list) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 cat account, got %+v", list)
	}
	if list[0].Username != "13900000001" || list[0].ShopName != "猫店" {
		t.Fatalf("account=%+v", list[0])
	}
	if !list[0].CookieValid {
		t.Fatalf("cookie should be valid after pull: %+v", list[0])
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(env.ts.URL + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		var snap status.Snapshot
		_ = json.NewDecoder(resp.Body).Decode(&snap)
		resp.Body.Close()
		if snap.PullPhase == status.PullSuccess && snap.PullCount == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("pull_phase should become success with count=1")
}

func seedAccount(t *testing.T, env accountsAPIEnv, user, pass string) string {
	t.Helper()
	a, err := env.acct.Add(user, pass)
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func getAccounts(t *testing.T, ts *httptest.Server) []status.AccountStatus {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/accounts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list status=%d body=%s", resp.StatusCode, body)
	}
	var list []status.AccountStatus
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("list JSON: %v body=%s", err, body)
	}
	return list
}
