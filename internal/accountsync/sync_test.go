package accountsync

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/example/jiaoyimao-scraper/credentialcrypto"
	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/leyoo"
)

func TestSyncCatBySupplierImportsAndRefresh(t *testing.T) {
	dir := t.TempDir()
	store := accounts.NewStore(filepath.Join(dir, "accounts.json"))
	_ = store.Load()

	crypto, err := credentialcrypto.New([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	encPhone, err := crypto.Encrypt("13800000001")
	if err != nil {
		t.Fatal(err)
	}
	encPass, err := crypto.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload := fmt.Sprintf(`{"code":0,"msg":"ok","data":[{
			"id":77,"name":"测试店","cookie":"ieu_member_uid=u1; ieu_member_token=tok1",
			"mobile":"","platform_key":"cat","supplier_id":1,
			"third_account":%q,"third_password":%q
		}]}`, encPhone, encPass)
		io.WriteString(w, payload)
	}))
	t.Cleanup(mock.Close)

	client := leyoo.NewClient(mock.URL)
	kept, err := SyncCatBySupplier(store, client, crypto, 1)
	if err != nil || kept != 1 {
		t.Fatalf("kept=%d err=%v", kept, err)
	}
	list := store.List()
	if len(list) != 1 || list[0].Username != "13800000001" {
		t.Fatalf("list=%+v", list)
	}

	_ = store.SetEnabled(list[0].ID, false, "Cookie 失效且重新拉取后仍无效")
	kept, err = SyncCatBySupplier(store, client, crypto, 1)
	if err != nil || kept != 1 {
		t.Fatalf("re-sync kept=%d err=%v", kept, err)
	}
	fresh, _ := store.Get(list[0].ID)
	if !fresh.Enabled {
		t.Fatalf("cookie-disabled account should re-enable: %+v", fresh)
	}

	cookies, err := RefreshAccountCookie(store, client, crypto, fresh)
	if err != nil || cookies == nil {
		t.Fatalf("refresh: %v cookies=%v", err, cookies)
	}
}
