package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/status"
)

func newAccountsAPI(t *testing.T) *httptest.Server {
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
	return ts
}

func TestAccountsAddAndListHidesPassword(t *testing.T) {
	ts := newAccountsAPI(t)

	resp, err := http.Post(ts.URL+"/api/accounts", "application/json",
		strings.NewReader(`{"username":"13800000000","password":"s3cret"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "s3cret") {
		t.Fatal("POST response leaked password")
	}

	listResp, err := http.Get(ts.URL + "/api/accounts")
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
	if got.ID == "" || got.Username != "13800000000" || !got.Enabled || !got.HasPassword {
		t.Fatalf("item=%+v", got)
	}
	if got.CookieValid {
		t.Fatal("cookie_valid should be false before import")
	}
}

func TestAccountsDelete(t *testing.T) {
	ts := newAccountsAPI(t)
	id := postAccount(t, ts, "u1", "p1")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/accounts/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status=%d", resp.StatusCode)
	}

	list := getAccounts(t, ts)
	if len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestAccountsImportCookiesSetsCookieValid(t *testing.T) {
	ts := newAccountsAPI(t)
	id := postAccount(t, ts, "cookieu", "pw")

	const cookiesJSON = `[{"name":"token","value":"abc","domain":".jiaoyimao.com","path":"/","expirationDate":1900000000,"httpOnly":true,"secure":true}]`
	resp, err := http.Post(ts.URL+"/api/accounts/"+id+"/cookies", "application/json", strings.NewReader(cookiesJSON))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status=%d body=%s", resp.StatusCode, body)
	}

	list := getAccounts(t, ts)
	if len(list) != 1 || !list[0].CookieValid {
		t.Fatalf("cookie_valid not set: %+v", list)
	}
}

func TestAccountLoginUnknownID(t *testing.T) {
	ts := newAccountsAPI(t)
	resp, err := http.Post(ts.URL+"/api/accounts/no-such-id/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

func TestAccountLoginStarts(t *testing.T) {
	ts := newAccountsAPI(t)
	id := postAccount(t, ts, "loginuser", "pw")

	resp, err := http.Post(ts.URL+"/api/accounts/"+id+"/login", "application/json", nil)
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
	ts := newAccountsAPI(t)

	resp, err := http.Post(ts.URL+"/api/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("0 accounts: status=%d body=%s", resp.StatusCode, body)
	}

	postAccount(t, ts, "a1", "p")
	postAccount(t, ts, "a2", "p")
	resp, err = http.Post(ts.URL+"/api/login", "application/json", nil)
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
	ts := newAccountsAPI(t)
	postAccount(t, ts, "only", "pw")

	resp, err := http.Post(ts.URL+"/api/login", "application/json", nil)
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
	ts := newAccountsAPI(t)
	postAccount(t, ts, "snapu", "pw")

	resp, err := http.Get(ts.URL + "/api/status")
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

func postAccount(t *testing.T, ts *httptest.Server, user, pass string) string {
	t.Helper()
	payload := `{"username":"` + user + `","password":"` + pass + `"}`
	resp, err := http.Post(ts.URL+"/api/accounts", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST add status=%d body=%s", resp.StatusCode, body)
	}
	var created status.AccountStatus
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("created JSON: %v body=%s", err, body)
	}
	if created.ID == "" {
		t.Fatalf("missing id in %s", body)
	}
	return created.ID
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
