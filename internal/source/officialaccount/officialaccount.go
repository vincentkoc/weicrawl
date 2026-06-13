package officialaccount

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/config"
)

const defaultBaseURL = "https://api.weixin.qq.com"

type Options struct {
	Config  config.OfficialAccountConfig
	BaseURL string
}

type Result struct {
	RunID             string   `json:"run_id"`
	Source            string   `json:"source"`
	Status            string   `json:"status"`
	TokenProbed       bool     `json:"token_probed"`
	TokenCacheSafe    bool     `json:"token_cache_safe"`
	RawTokenPersisted bool     `json:"raw_token_persisted"`
	ExpiresIn         int64    `json:"expires_in,omitempty"`
	Accounts          int64    `json:"accounts,omitempty"`
	Articles          int64    `json:"articles,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	ErrCode     int64  `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

type tokenCacheState struct {
	ExpiresIn         int64  `json:"expires_in"`
	ExpiresAt         string `json:"expires_at,omitempty"`
	Policy            string `json:"policy"`
	RawTokenPersisted bool   `json:"raw_token_persisted"`
}

type materialListResponse struct {
	TotalCount int64          `json:"total_count"`
	ItemCount  int64          `json:"item_count"`
	Items      []materialItem `json:"item"`
	ErrCode    int64          `json:"errcode"`
	ErrMsg     string         `json:"errmsg"`
}

type materialItem struct {
	MediaID    string          `json:"media_id"`
	Content    materialContent `json:"content"`
	UpdateTime int64           `json:"update_time"`
}

type materialContent struct {
	NewsItems []newsItem `json:"news_item"`
}

type newsItem struct {
	Title            string `json:"title"`
	Author           string `json:"author"`
	Digest           string `json:"digest"`
	Content          string `json:"content"`
	URL              string `json:"url"`
	ContentSourceURL string `json:"content_source_url"`
	ThumbMediaID     string `json:"thumb_media_id"`
}

func Sync(ctx context.Context, arc *archive.Archive, opts Options) (Result, error) {
	started := time.Now().UTC()
	runID := "official-" + started.Format("20060102T150405.000000000Z")
	result := Result{RunID: runID, Source: "official-account-api", Status: "skipped", TokenCacheSafe: true}
	appID := strings.TrimSpace(os.Getenv(opts.Config.AppIDEnv))
	appSecret := strings.TrimSpace(os.Getenv(opts.Config.AppSecretEnv))
	if appID == "" || appSecret == "" {
		result.Warnings = append(result.Warnings, "official-account credentials are missing")
		return result, arc.InsertSyncRun(ctx, archive.SyncRun{
			RunID:      runID,
			Source:     result.Source,
			StartedAt:  started.Format(time.RFC3339),
			FinishedAt: time.Now().UTC().Format(time.RFC3339),
			Status:     result.Status,
			Warnings:   result.Warnings,
		})
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	token, err := fetchAccessToken(ctx, baseURL, appID, appSecret)
	if err != nil {
		err = redactOfficialError(err)
		result.Status = "failed"
		result.Warnings = append(result.Warnings, err.Error())
		_ = arc.InsertSyncRun(ctx, archive.SyncRun{
			RunID:      runID,
			Source:     result.Source,
			StartedAt:  started.Format(time.RFC3339),
			FinishedAt: time.Now().UTC().Format(time.RFC3339),
			Status:     result.Status,
			Warnings:   result.Warnings,
		})
		return result, err
	}
	result.Status = "success"
	result.TokenProbed = true
	result.ExpiresIn = token.ExpiresIn
	if err := arc.UpsertProfile(ctx, "official-account", appID, "Official Account", baseURL, "", map[string]any{"source": "official-account-api", "app_id_configured": true}); err != nil {
		return result, err
	}
	rawAccount, _ := json.Marshal(map[string]any{"source": "official-account-api", "app_id_configured": true})
	if err := arc.UpsertBizAccount(ctx, archive.BizAccount{
		ProfileID:   "official-account",
		AccountID:   appID,
		DisplayName: appID,
		RawJSON:     string(rawAccount),
	}); err != nil {
		return result, err
	}
	result.Accounts = 1
	materials, err := fetchNewsMaterials(ctx, baseURL, token.AccessToken, 0, 20)
	if err != nil {
		err = redactOfficialError(err)
		result.Status = "partial"
		result.Warnings = append(result.Warnings, err.Error())
	} else {
		for _, item := range materials.Items {
			for idx, news := range item.Content.NewsItems {
				id := item.MediaID + ":" + strconv.Itoa(idx)
				if item.MediaID == "" {
					id = articleID(news.URL, news.Title, idx)
				}
				raw, _ := json.Marshal(map[string]any{"media_id": item.MediaID, "update_time": item.UpdateTime, "news": news})
				if err := arc.UpsertArticle(ctx, archive.Article{
					ProfileID:   "official-account",
					ArticleID:   id,
					AccountID:   appID,
					Title:       firstNonEmpty(news.Title, news.URL, id),
					URL:         news.URL,
					Summary:     news.Digest,
					PublishedAt: unixSeconds(item.UpdateTime),
					RawJSON:     string(raw),
				}); err != nil {
					return result, err
				}
				result.Articles++
			}
		}
	}
	state, err := marshalTokenCacheState(token, time.Now().UTC())
	if err != nil {
		return result, err
	}
	if _, err := arc.DB().ExecContext(ctx, `insert or replace into sync_state(source_name, entity_type, entity_id, value, updated_at) values('official-account-api', 'token', 'access_token', ?, ?)`, state, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	return result, arc.InsertSyncRun(ctx, archive.SyncRun{
		RunID:               runID,
		Source:              result.Source,
		ProfileID:           "official-account",
		StartedAt:           started.Format(time.RFC3339),
		FinishedAt:          time.Now().UTC().Format(time.RFC3339),
		Status:              result.Status,
		ImportedProfiles:    1,
		ImportedBizAccounts: result.Accounts,
		ImportedArticles:    result.Articles,
		Warnings:            result.Warnings,
	})
}

func marshalTokenCacheState(token tokenResponse, observedAt time.Time) (string, error) {
	state := tokenCacheState{
		ExpiresIn:         token.ExpiresIn,
		Policy:            "metadata-only",
		RawTokenPersisted: false,
	}
	if token.ExpiresIn > 0 {
		state.ExpiresAt = observedAt.Add(time.Duration(token.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	bytes, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

var officialSecretRE = regexp.MustCompile(`(?i)(access_token|secret)=([^&\s"]+)`)

func redactOfficialError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", officialSecretRE.ReplaceAllString(err.Error(), `${1}=[redacted]`))
}

func fetchNewsMaterials(ctx context.Context, baseURL, accessToken string, offset, count int) (materialListResponse, error) {
	endpoint, err := url.Parse(baseURL + "/cgi-bin/material/batchget_material")
	if err != nil {
		return materialListResponse{}, err
	}
	query := endpoint.Query()
	query.Set("access_token", accessToken)
	endpoint.RawQuery = query.Encode()
	body, _ := json.Marshal(map[string]any{"type": "news", "offset": offset, "count": count})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return materialListResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return materialListResponse{}, fmt.Errorf("fetch official-account materials: %w", err)
	}
	defer resp.Body.Close()
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return materialListResponse{}, err
	}
	var materials materialListResponse
	if err := json.Unmarshal(bytes, &materials); err != nil {
		return materialListResponse{}, fmt.Errorf("decode official-account materials: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return materialListResponse{}, fmt.Errorf("official-account materials HTTP %d", resp.StatusCode)
	}
	if materials.ErrCode != 0 {
		return materialListResponse{}, fmt.Errorf("official-account materials error %d: %s", materials.ErrCode, materials.ErrMsg)
	}
	return materials, nil
}

func articleID(url, title string, idx int) string {
	sum := sha256.Sum256([]byte(url + "\x00" + title + "\x00" + strconv.Itoa(idx)))
	return hex.EncodeToString(sum[:])
}

func unixSeconds(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func fetchAccessToken(ctx context.Context, baseURL, appID, appSecret string) (tokenResponse, error) {
	endpoint, err := url.Parse(baseURL + "/cgi-bin/token")
	if err != nil {
		return tokenResponse{}, err
	}
	query := endpoint.Query()
	query.Set("grant_type", "client_credential")
	query.Set("appid", appID)
	query.Set("secret", appSecret)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return tokenResponse{}, err
	}
	client := http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("fetch official-account token: %w", err)
	}
	defer resp.Body.Close()
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return tokenResponse{}, fmt.Errorf("decode official-account token: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("official-account token HTTP %d", resp.StatusCode)
	}
	if token.ErrCode != 0 {
		return tokenResponse{}, fmt.Errorf("official-account token error %d: %s", token.ErrCode, token.ErrMsg)
	}
	if token.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("official-account token response did not include access_token")
	}
	return token, nil
}
