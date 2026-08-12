package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetTenantAccessToken(t *testing.T) {
	var tokenCalls counter
	srv := httptest.NewServer(tokenHandler(&tokenCalls))
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	token, err := c.GetTenantAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetTenantAccessToken: %v", err)
	}
	if token != "t1" {
		t.Fatalf("token = %q, want t1", token)
	}

	// 第二次调用应命中缓存，不再请求
	token2, err := c.GetTenantAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetTenantAccessToken (cached): %v", err)
	}
	if token2 != token {
		t.Fatalf("cached token = %q, want %q", token2, token)
	}
	if got := tokenCalls.Get(); got != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (cached)", got)
	}
}

func TestGetTenantAccessTokenExpiredRefreshes(t *testing.T) {
	var tokenCalls counter
	srv := httptest.NewServer(tokenHandler(&tokenCalls))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if _, err := c.GetTenantAccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 手动让缓存过期
	c.mu.Lock()
	c.tokenExp = time.Now().Add(-time.Minute)
	c.mu.Unlock()

	token, err := c.GetTenantAccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "t2" {
		t.Fatalf("token = %q, want t2 (refreshed)", token)
	}
	if got := tokenCalls.Get(); got != 2 {
		t.Fatalf("token endpoint called %d times, want 2", got)
	}
}

func TestGetTenantAccessTokenAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":99991661,"msg":"app secret 错误"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetTenantAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for non-zero code")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Code != 99991661 {
		t.Fatalf("code = %d, want 99991661", apiErr.Code)
	}
}

func TestDoRequestSuccess(t *testing.T) {
	var tokenCalls counter
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			tokenHandler(&tokenCalls)(w, r)
		case "/test/path":
			apiCalled.Add()
			if got := r.Header.Get("Authorization"); got != "Bearer t1" {
				t.Errorf("Authorization = %q, want Bearer t1", got)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"records":[{"record_id":"rec1"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	var result struct {
		Data struct {
			Records []struct {
				RecordID string `json:"record_id"`
			} `json:"records"`
		} `json:"data"`
	}
	if err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, &result); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if len(result.Data.Records) != 1 || result.Data.Records[0].RecordID != "rec1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if apiCalled.Get() != 1 {
		t.Fatalf("api called %d times, want 1", apiCalled.Get())
	}
}

func TestDoRequestBusinessErrorNotRetried(t *testing.T) {
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"tok","expire":7200}`)
		case "/test/path":
			apiCalled.Add()
			fmt.Fprint(w, `{"code":1254041,"msg":"表不存在"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 1254041 {
		t.Fatalf("err = %v, want APIError code 1254041", err)
	}
	if apiCalled.Get() != 1 {
		t.Fatalf("api called %d times, want 1 (non-retryable)", apiCalled.Get())
	}
}

func TestDoRequestRetryOnHTTP500(t *testing.T) {
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"tok","expire":7200}`)
		case "/test/path":
			apiCalled.Add()
			if apiCalled.Get() < 3 {
				http.Error(w, `{"code":99999999,"msg":"internal"}`, http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, nil); err != nil {
		t.Fatalf("doRequest after retries: %v", err)
	}
	if apiCalled.Get() != 3 {
		t.Fatalf("api called %d times, want 3", apiCalled.Get())
	}
}

func TestDoRequestRetryOnThrottle(t *testing.T) {
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"tok","expire":7200}`)
		case "/test/path":
			apiCalled.Add()
			if apiCalled.Get() == 1 {
				fmt.Fprint(w, `{"code":99991400,"msg":"throttled"}`)
				return
			}
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, nil); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if apiCalled.Get() != 2 {
		t.Fatalf("api called %d times, want 2", apiCalled.Get())
	}
}

func TestDoRequestRefreshesTokenOnInvalid(t *testing.T) {
	var tokenCalls counter
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			tokenHandler(&tokenCalls)(w, r)
		case "/test/path":
			apiCalled.Add()
			// 第一次携带旧 token t1 → 无效；第二次携带新 token t2 → 成功
			if r.Header.Get("Authorization") == "Bearer t1" {
				fmt.Fprint(w, `{"code":99991663,"msg":"token invalid"}`)
				return
			}
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, nil); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if tokenCalls.Get() != 2 {
		t.Fatalf("token endpoint called %d times, want 2 (refreshed)", tokenCalls.Get())
	}
	if apiCalled.Get() != 2 {
		t.Fatalf("api called %d times, want 2", apiCalled.Get())
	}
}

func TestDoRequestExhaustsRetries(t *testing.T) {
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"tok","expire":7200}`)
		case "/test/path":
			apiCalled.Add()
			http.Error(w, `{"code":1,"msg":"oops"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if apiCalled.Get() != 3 {
		t.Fatalf("api called %d times, want 3 (maxRetries)", apiCalled.Get())
	}
}

func TestDoRequestRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"tok","expire":7200}`)
		case "/test/path":
			time.Sleep(300 * time.Millisecond)
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.doRequest(ctx, http.MethodGet, "/test/path", nil, nil)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("doRequest took %v despite cancellation, retries not respecting ctx", elapsed)
	}
}

func TestRateLimitMinInterval(t *testing.T) {
	var apiCalled counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"tok","expire":7200}`)
		case "/test/path":
			apiCalled.Add()
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.minInterval = 100 * time.Millisecond

	start := time.Now()
	for i := 0; i < 2; i++ {
		if err := c.doRequest(context.Background(), http.MethodGet, "/test/path", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("two requests completed in %v, want >= ~100ms (rate limited)", elapsed)
	}
	if apiCalled.Get() != 2 {
		t.Fatalf("api called %d times, want 2", apiCalled.Get())
	}
}
