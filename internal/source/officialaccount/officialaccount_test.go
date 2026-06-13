package officialaccount

import (
	"context"
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
	if result.Articles != 1 || result.Status != "success" {
		t.Fatalf("result = %#v", result)
	}
	status, err := arc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicAccountArticleCount != 1 {
		t.Fatalf("article count = %d", status.PublicAccountArticleCount)
	}
	rows, err := arc.Query(context.Background(), `select value from sync_state where source_name='official-account-api' and entity_id='access_token'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Values) != 1 || strings.Contains(rows.Values[0]["value"].(string), "token-value") {
		t.Fatalf("sync state leaked token: %#v", rows.Values)
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
