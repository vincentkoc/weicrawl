package officialaccount

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/config"
)

func TestSyncFetchesNewsMaterialsWithoutPersistingToken(t *testing.T) {
	var sawMaterialToken bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			if r.URL.Query().Get("secret") != "secret" {
				t.Fatalf("secret query = %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token-value","expires_in":7200}`))
		case "/cgi-bin/material/batchget_material":
			sawMaterialToken = r.URL.Query().Get("access_token") == "token-value"
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "total_count": 1,
  "item_count": 1,
  "item": [{
    "media_id": "media-1",
    "update_time": 1781323200,
    "content": {
      "news_item": [{
        "title": "Launch note",
        "digest": "A short official-account post",
        "url": "https://example.invalid/post"
      }]
    }
  }]
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("APP_ID_ENV", "app")
	t.Setenv("APP_SECRET_ENV", "secret")
	arc, err := archive.Open(context.Background(), filepath.Join(t.TempDir(), "weicrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	result, err := Sync(context.Background(), arc, Options{
		Config:  config.OfficialAccountConfig{AppIDEnv: "APP_ID_ENV", AppSecretEnv: "APP_SECRET_ENV"},
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawMaterialToken {
		t.Fatal("material request did not use fetched token")
	}
	if result.Accounts != 1 || result.Articles != 1 || result.Status != "success" {
		t.Fatalf("result = %#v", result)
	}
	if !result.TokenCacheSafe || result.RawTokenPersisted {
		t.Fatalf("unsafe token cache posture: %#v", result)
	}
	status, err := arc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicAccountArticleCount != 1 {
		t.Fatalf("article count = %d", status.PublicAccountArticleCount)
	}
	if status.BizAccountCount != 1 {
		t.Fatalf("biz account count = %d", status.BizAccountCount)
	}
	rows, err := arc.Query(context.Background(), `select value from sync_state where source_name='official-account-api' and entity_id='access_token'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Values) != 1 || strings.Contains(rows.Values[0]["value"].(string), "token-value") {
		t.Fatalf("sync state leaked token: %#v", rows.Values)
	}
	var tokenState struct {
		ExpiresIn         int64  `json:"expires_in"`
		ExpiresAt         string `json:"expires_at"`
		Policy            string `json:"policy"`
		RawTokenPersisted bool   `json:"raw_token_persisted"`
	}
	if err := json.Unmarshal([]byte(rows.Values[0]["value"].(string)), &tokenState); err != nil {
		t.Fatalf("sync state is not structured token metadata: %v", err)
	}
	if tokenState.ExpiresIn != 7200 || tokenState.ExpiresAt == "" || tokenState.Policy != "metadata-only" || tokenState.RawTokenPersisted {
		t.Fatalf("unexpected token cache state: %#v", tokenState)
	}
}

func TestSyncRedactsOfficialAccountSecretFromTokenErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()
	t.Setenv("APP_ID_ENV", "app")
	t.Setenv("APP_SECRET_ENV", "secret-value")
	arc, err := archive.Open(context.Background(), filepath.Join(t.TempDir(), "weicrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	result, err := Sync(context.Background(), arc, Options{
		Config:  config.OfficialAccountConfig{AppIDEnv: "APP_ID_ENV", AppSecretEnv: "APP_SECRET_ENV"},
		BaseURL: baseURL,
	})
	if err == nil {
		t.Fatal("expected token fetch error")
	}
	for _, text := range []string{err.Error(), strings.Join(result.Warnings, "\n")} {
		if strings.Contains(text, "secret-value") {
			t.Fatalf("secret leaked in error text: %s", text)
		}
		if !strings.Contains(text, "secret=[redacted]") {
			t.Fatalf("redaction missing from error text: %s", text)
		}
	}
}

func TestSyncReportsOfficialAccountMaterialRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token-value","expires_in":7200}`))
		case "/cgi-bin/material/batchget_material":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errcode":45009,"errmsg":"api freq out of limit"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("APP_ID_ENV", "app")
	t.Setenv("APP_SECRET_ENV", "secret")
	arc, err := archive.Open(context.Background(), filepath.Join(t.TempDir(), "weicrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	result, err := Sync(context.Background(), arc, Options{
		Config:  config.OfficialAccountConfig{AppIDEnv: "APP_ID_ENV", AppSecretEnv: "APP_SECRET_ENV"},
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial" || !result.RateLimited || result.RetryAfter != "60" || result.ErrCode != 45009 {
		t.Fatalf("rate limit posture missing: %#v", result)
	}
	warnings := strings.Join(result.Warnings, "\n")
	if !strings.Contains(warnings, "rate limited") || !strings.Contains(warnings, "retry_after=60") {
		t.Fatalf("rate limit warning missing: %s", warnings)
	}
	if strings.Contains(warnings, "token-value") {
		t.Fatalf("token leaked in warning: %s", warnings)
	}
}

func TestSyncReportsOfficialAccountTokenRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/token" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "120")
		_, _ = w.Write([]byte(`{"errcode":45009,"errmsg":"api freq out of limit"}`))
	}))
	defer server.Close()

	t.Setenv("APP_ID_ENV", "app")
	t.Setenv("APP_SECRET_ENV", "secret")
	arc, err := archive.Open(context.Background(), filepath.Join(t.TempDir(), "weicrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	result, err := Sync(context.Background(), arc, Options{
		Config:  config.OfficialAccountConfig{AppIDEnv: "APP_ID_ENV", AppSecretEnv: "APP_SECRET_ENV"},
		BaseURL: server.URL,
	})
	if err == nil {
		t.Fatal("expected token rate limit error")
	}
	if result.Status != "failed" || !result.RateLimited || result.RetryAfter != "120" || result.ErrCode != 45009 {
		t.Fatalf("token rate limit posture missing: result=%#v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "rate limited") || !strings.Contains(err.Error(), "retry_after=120") {
		t.Fatalf("rate limit error missing retry posture: %v", err)
	}
}

func TestFetchAccessTokenReportsPlainHTTPRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("too frequent"))
	}))
	defer server.Close()

	_, err := fetchAccessToken(context.Background(), server.URL, "app", "secret")
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected apiError, got %T %v", err, err)
	}
	if !apiErr.RateLimited || apiErr.RetryAfter != "30" || apiErr.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("plain HTTP rate limit not classified: %#v", apiErr)
	}
}

func TestFetchNewsMaterialsRedactsAccessTokenFromErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()
	_, err := fetchNewsMaterials(context.Background(), baseURL, "token-value", 0, 20)
	if err == nil {
		t.Fatal("expected material fetch error")
	}
	err = redactOfficialError(err)
	if strings.Contains(err.Error(), "token-value") {
		t.Fatalf("token leaked in error text: %s", err)
	}
	tokenParam := "access_" + "token"
	if !strings.Contains(err.Error(), tokenParam+"=[redacted]") {
		t.Fatalf("redaction missing from error text: %s", err)
	}
}
