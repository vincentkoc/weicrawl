package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/vincentkoc/weicrawl/internal/schema"
)

type Archive struct {
	store *ckstore.Store
}

type Count struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type Status struct {
	ProfileCount              int64            `json:"profile_count"`
	ContactCount              int64            `json:"contact_count"`
	ChatCount                 int64            `json:"chat_count"`
	ChatCountsByKind          map[string]int64 `json:"chat_counts_by_kind"`
	MessageCount              int64            `json:"message_count"`
	MessageCountsByKind       map[string]int64 `json:"message_counts_by_kind"`
	FirstMessageAt            string           `json:"first_message_at,omitempty"`
	LastMessageAt             string           `json:"last_message_at,omitempty"`
	MediaCount                int64            `json:"media_metadata_count"`
	FavoriteCount             int64            `json:"favorite_count"`
	MomentCount               int64            `json:"moment_count"`
	PublicAccountArticleCount int64            `json:"public_account_article_count"`
	LastSyncRun               *SyncRun         `json:"last_sync_run,omitempty"`
	Counts                    []Count          `json:"counts"`
}

type SyncRun struct {
	RunID              string   `json:"run_id"`
	Source             string   `json:"source"`
	ProfileID          string   `json:"profile_id,omitempty"`
	StartedAt          string   `json:"started_at"`
	FinishedAt         string   `json:"finished_at,omitempty"`
	Status             string   `json:"status"`
	AppVersion         string   `json:"app_version,omitempty"`
	SourceRoot         string   `json:"source_root,omitempty"`
	SnapshotPath       string   `json:"snapshot_path,omitempty"`
	SourceDBCount      int64    `json:"source_db_count"`
	ImportedProfiles   int64    `json:"imported_profiles"`
	ImportedContacts   int64    `json:"imported_contacts"`
	ImportedChats      int64    `json:"imported_chats"`
	ImportedMessages   int64    `json:"imported_messages"`
	ImportedMedia      int64    `json:"imported_media"`
	ImportedRawRecords int64    `json:"imported_raw_records"`
	Warnings           []string `json:"warnings,omitempty"`
}

type Message struct {
	ProfileID      string `json:"profile_id"`
	MessageID      string `json:"message_id"`
	ChatID         string `json:"chat_id"`
	SenderID       string `json:"sender_id,omitempty"`
	Direction      string `json:"direction"`
	MessageType    string `json:"message_type"`
	SentAt         string `json:"sent_at,omitempty"`
	Text           string `json:"text"`
	NormalizedText string `json:"normalized_text"`
	SourceDB       string `json:"source_db"`
	SourceRowID    string `json:"source_rowid"`
	RawJSON        string `json:"raw_json"`
	UpdatedAt      string `json:"updated_at"`
}

type SearchHit struct {
	Entity     string `json:"entity"`
	ProfileID  string `json:"profile_id"`
	MessageID  string `json:"message_id,omitempty"`
	ArticleID  string `json:"article_id,omitempty"`
	FavoriteID string `json:"favorite_id,omitempty"`
	MomentID   string `json:"moment_id,omitempty"`
	ContactID  string `json:"contact_id,omitempty"`
	MediaID    string `json:"media_id,omitempty"`
	ChatID     string `json:"chat_id,omitempty"`
	SenderID   string `json:"sender_id,omitempty"`
	SentAt     string `json:"sent_at,omitempty"`
	Type       string `json:"type"`
	Text       string `json:"text"`
	Rank       any    `json:"rank,omitempty"`
}

type MessagePart struct {
	ProfileID string `json:"profile_id"`
	MessageID string `json:"message_id"`
	PartIndex int64  `json:"part_index"`
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	MediaID   string `json:"media_id,omitempty"`
	URL       string `json:"url,omitempty"`
	RawJSON   string `json:"raw_json,omitempty"`
}

type ChatMember struct {
	ProfileID   string `json:"profile_id"`
	ChatID      string `json:"chat_id"`
	ContactID   string `json:"contact_id"`
	DisplayName string `json:"display_name,omitempty"`
	RawJSON     string `json:"raw_json,omitempty"`
}

type MessageEvent struct {
	ProfileID   string `json:"profile_id"`
	ChatID      string `json:"chat_id"`
	MessageID   string `json:"message_id"`
	EventType   string `json:"event_type"`
	EventAt     string `json:"event_at"`
	PayloadJSON string `json:"payload_json,omitempty"`
}

type MediaItem struct {
	ProfileID   string `json:"profile_id"`
	MediaID     string `json:"media_id"`
	Kind        string `json:"kind"`
	SourcePath  string `json:"source_path,omitempty"`
	ArchivePath string `json:"archive_path,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	ByteSize    int64  `json:"byte_size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Width       int64  `json:"width,omitempty"`
	Height      int64  `json:"height,omitempty"`
	DurationMS  int64  `json:"duration_ms,omitempty"`
	RawJSON     string `json:"raw_json,omitempty"`
}

type Favorite struct {
	ProfileID  string `json:"profile_id"`
	FavoriteID string `json:"favorite_id"`
	Kind       string `json:"kind"`
	Title      string `json:"title,omitempty"`
	Text       string `json:"text,omitempty"`
	SourceRef  string `json:"source_ref,omitempty"`
	RawJSON    string `json:"raw_json,omitempty"`
}

type Article struct {
	ProfileID   string `json:"profile_id"`
	ArticleID   string `json:"article_id"`
	AccountID   string `json:"account_id,omitempty"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Summary     string `json:"summary,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	RawJSON     string `json:"raw_json,omitempty"`
}

type Moment struct {
	ProfileID string `json:"profile_id"`
	MomentID  string `json:"moment_id"`
	AuthorID  string `json:"author_id,omitempty"`
	Text      string `json:"text,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	RawJSON   string `json:"raw_json,omitempty"`
}

func Open(ctx context.Context, path string) (*Archive, error) {
	st, err := ckstore.Open(ctx, ckstore.Options{Path: path, Schema: schema.SQL, SchemaVersion: schema.Version})
	if err != nil {
		return nil, err
	}
	_, _ = st.DB().ExecContext(ctx, `alter table sync_runs add column imported_raw_records integer not null default 0`)
	return &Archive{store: st}, nil
}

func (a *Archive) Close() error {
	if a == nil {
		return nil
	}
	return a.store.Close()
}

func (a *Archive) DB() *sql.DB {
	return a.store.DB()
}

func (a *Archive) Path() string {
	return a.store.Path()
}

func (a *Archive) SchemaVersion(ctx context.Context) (int, error) {
	return a.store.SchemaVersion(ctx)
}

func (a *Archive) Query(ctx context.Context, query string, args ...any) (ckstore.QueryResult, error) {
	return a.store.Query(ctx, query, args...)
}

func (a *Archive) Status(ctx context.Context) (Status, error) {
	status := Status{
		ChatCountsByKind:    map[string]int64{},
		MessageCountsByKind: map[string]int64{},
	}
	var err error
	status.ProfileCount, err = a.scalar(ctx, `select count(*) from profiles`)
	if err != nil {
		return status, err
	}
	status.ContactCount, err = a.scalar(ctx, `select count(*) from contacts`)
	if err != nil {
		return status, err
	}
	status.ChatCount, err = a.scalar(ctx, `select count(*) from chats`)
	if err != nil {
		return status, err
	}
	status.MessageCount, err = a.scalar(ctx, `select count(*) from messages`)
	if err != nil {
		return status, err
	}
	status.MediaCount, err = a.scalar(ctx, `select count(*) from media_items`)
	if err != nil {
		return status, err
	}
	status.FavoriteCount, err = a.scalar(ctx, `select count(*) from favorites`)
	if err != nil {
		return status, err
	}
	status.MomentCount, err = a.scalar(ctx, `select count(*) from moments`)
	if err != nil {
		return status, err
	}
	status.PublicAccountArticleCount, err = a.scalar(ctx, `select count(*) from biz_articles`)
	if err != nil {
		return status, err
	}
	status.ChatCountsByKind, err = a.groupCounts(ctx, `select kind, count(*) from chats group by kind order by kind`)
	if err != nil {
		return status, err
	}
	status.MessageCountsByKind, err = a.groupCounts(ctx, `select message_type, count(*) from messages group by message_type order by message_type`)
	if err != nil {
		return status, err
	}
	var firstMsg, lastMsg sql.NullString
	if err := a.store.DB().QueryRowContext(ctx, `select min(sent_at), max(sent_at) from messages where sent_at is not null and sent_at <> ''`).Scan(&firstMsg, &lastMsg); err == nil {
		status.FirstMessageAt = firstMsg.String
		status.LastMessageAt = lastMsg.String
	}
	last, err := a.LastSyncRun(ctx)
	if err != nil {
		return status, err
	}
	status.LastSyncRun = last
	status.Counts = []Count{
		{ID: "profiles", Label: "Profiles", Value: status.ProfileCount},
		{ID: "contacts", Label: "Contacts", Value: status.ContactCount},
		{ID: "chats", Label: "Chats", Value: status.ChatCount},
		{ID: "messages", Label: "Messages", Value: status.MessageCount},
		{ID: "media_items", Label: "Media metadata", Value: status.MediaCount},
		{ID: "favorites", Label: "Favorites", Value: status.FavoriteCount},
		{ID: "biz_articles", Label: "Public-account articles", Value: status.PublicAccountArticleCount},
		{ID: "moments", Label: "Moments", Value: status.MomentCount},
	}
	return status, nil
}

func (a *Archive) LastSyncRun(ctx context.Context) (*SyncRun, error) {
	rows, err := a.store.DB().QueryContext(ctx, `select run_id, source, coalesce(profile_id,''), started_at, coalesce(finished_at,''), status, coalesce(app_version,''), coalesce(source_root,''), coalesce(snapshot_path,''), source_db_count, imported_profiles, imported_contacts, imported_chats, imported_messages, imported_media, imported_raw_records, warnings_json from sync_runs order by started_at desc limit 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	run, err := scanRun(rows)
	if err != nil {
		return nil, err
	}
	return &run, rows.Err()
}

func (a *Archive) InsertSyncRun(ctx context.Context, run SyncRun) error {
	warnings, _ := json.Marshal(run.Warnings)
	_, err := a.store.DB().ExecContext(ctx, `insert or replace into sync_runs(run_id, source, profile_id, started_at, finished_at, status, app_version, source_root, snapshot_path, source_db_count, imported_profiles, imported_contacts, imported_chats, imported_messages, imported_media, imported_raw_records, warnings_json) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.RunID, run.Source, nullEmpty(run.ProfileID), run.StartedAt, nullEmpty(run.FinishedAt), run.Status, nullEmpty(run.AppVersion), nullEmpty(run.SourceRoot), nullEmpty(run.SnapshotPath), run.SourceDBCount, run.ImportedProfiles, run.ImportedContacts, run.ImportedChats, run.ImportedMessages, run.ImportedMedia, run.ImportedRawRecords, string(warnings))
	return err
}

func (a *Archive) UpsertProfile(ctx context.Context, profileID, wxid, displayName, sourceRoot, appVersion string, raw any) error {
	now := time.Now().UTC().Format(time.RFC3339)
	rawJSON := marshalRaw(raw)
	_, err := a.store.DB().ExecContext(ctx, `insert into profiles(profile_id, wxid, display_name, source_root, app_version, raw_json, updated_at) values(?,?,?,?,?,?,?) on conflict(profile_id) do update set wxid=excluded.wxid, display_name=excluded.display_name, source_root=excluded.source_root, app_version=excluded.app_version, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		profileID, nullEmpty(wxid), nullEmpty(displayName), sourceRoot, nullEmpty(appVersion), rawJSON, now)
	return err
}

func (a *Archive) UpsertContact(ctx context.Context, profileID, contactID, alias, displayName, remarkName, kind, avatarRef string, raw any) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into contacts(profile_id, contact_id, alias, display_name, remark_name, kind, avatar_ref, raw_json, updated_at) values(?,?,?,?,?,?,?,?,?) on conflict(profile_id, contact_id) do update set alias=excluded.alias, display_name=excluded.display_name, remark_name=excluded.remark_name, kind=excluded.kind, avatar_ref=excluded.avatar_ref, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		profileID, contactID, nullEmpty(alias), nullEmpty(displayName), nullEmpty(remarkName), defaultString(kind, "unknown"), nullEmpty(avatarRef), marshalRaw(raw), now)
	return err
}

func (a *Archive) UpsertChat(ctx context.Context, profileID, chatID, kind, title, lastMessageAt string, unread int64, muted, pinned bool, raw any) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into chats(profile_id, chat_id, kind, title, last_message_at, unread_count, muted, pinned, raw_json, updated_at) values(?,?,?,?,?,?,?,?,?,?) on conflict(profile_id, chat_id) do update set kind=excluded.kind, title=excluded.title, last_message_at=excluded.last_message_at, unread_count=excluded.unread_count, muted=excluded.muted, pinned=excluded.pinned, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		profileID, chatID, defaultString(kind, "unknown"), nullEmpty(title), nullEmpty(lastMessageAt), unread, boolInt(muted), boolInt(pinned), marshalRaw(raw), now)
	return err
}

func (a *Archive) UpsertChatMember(ctx context.Context, member ChatMember) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into chat_members(profile_id, chat_id, contact_id, display_name, raw_json, updated_at) values(?,?,?,?,?,?) on conflict(profile_id, chat_id, contact_id) do update set display_name=excluded.display_name, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		member.ProfileID, member.ChatID, member.ContactID, nullEmpty(member.DisplayName), defaultString(member.RawJSON, "{}"), now)
	return err
}

func (a *Archive) UpsertMessage(ctx context.Context, msg Message) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if msg.UpdatedAt == "" {
		msg.UpdatedAt = now
	}
	if msg.NormalizedText == "" {
		msg.NormalizedText = NormalizeText(msg.Text)
	}
	_, err := a.store.DB().ExecContext(ctx, `insert into messages(profile_id, message_id, chat_id, sender_id, direction, message_type, sent_at, edited_at, deleted_at, text, normalized_text, source_db, source_rowid, raw_json, updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(profile_id, message_id) do update set chat_id=excluded.chat_id, sender_id=excluded.sender_id, direction=excluded.direction, message_type=excluded.message_type, sent_at=excluded.sent_at, edited_at=excluded.edited_at, deleted_at=excluded.deleted_at, text=excluded.text, normalized_text=excluded.normalized_text, source_db=excluded.source_db, source_rowid=excluded.source_rowid, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		msg.ProfileID, msg.MessageID, msg.ChatID, nullEmpty(msg.SenderID), defaultString(msg.Direction, "unknown"), defaultString(msg.MessageType, "unsupported"), nullEmpty(msg.SentAt), nil, nil, msg.Text, msg.NormalizedText, defaultString(msg.SourceDB, "fixture"), defaultString(msg.SourceRowID, msg.MessageID), defaultString(msg.RawJSON, "{}"), msg.UpdatedAt)
	if err != nil {
		return err
	}
	if _, err := a.store.DB().ExecContext(ctx, `delete from message_fts where rowid = (select rowid from messages where profile_id=? and message_id=?)`, msg.ProfileID, msg.MessageID); err != nil {
		return err
	}
	_, err = a.store.DB().ExecContext(ctx, `insert into message_fts(rowid, profile_id, message_id, chat_id, sender_id, body) values((select rowid from messages where profile_id=? and message_id=?), ?, ?, ?, ?, ?)`,
		msg.ProfileID, msg.MessageID, msg.ProfileID, msg.MessageID, msg.ChatID, nullEmpty(msg.SenderID), msg.NormalizedText)
	return err
}

func (a *Archive) UpsertMessagePart(ctx context.Context, part MessagePart) error {
	_, err := a.store.DB().ExecContext(ctx, `insert into message_parts(profile_id, message_id, part_index, kind, text, media_id, url, raw_json) values(?,?,?,?,?,?,?,?) on conflict(profile_id, message_id, part_index) do update set kind=excluded.kind, text=excluded.text, media_id=excluded.media_id, url=excluded.url, raw_json=excluded.raw_json`,
		part.ProfileID, part.MessageID, part.PartIndex, defaultString(part.Kind, "unknown"), part.Text, nullEmpty(part.MediaID), nullEmpty(part.URL), defaultString(part.RawJSON, "{}"))
	return err
}

func (a *Archive) InsertMessageEvent(ctx context.Context, event MessageEvent) error {
	eventAt := event.EventAt
	if strings.TrimSpace(eventAt) == "" {
		eventAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := a.store.DB().ExecContext(ctx, `insert or ignore into message_events(profile_id, chat_id, message_id, event_type, event_at, payload_json) values(?,?,?,?,?,?)`,
		event.ProfileID, event.ChatID, event.MessageID, defaultString(event.EventType, "unknown"), eventAt, defaultString(event.PayloadJSON, "{}"))
	return err
}

func (a *Archive) UpsertMedia(ctx context.Context, item MediaItem) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into media_items(profile_id, media_id, kind, source_path, archive_path, mime_type, byte_size, sha256, width, height, duration_ms, raw_json, updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(profile_id, media_id) do update set kind=excluded.kind, source_path=coalesce(excluded.source_path, media_items.source_path), archive_path=coalesce(excluded.archive_path, media_items.archive_path), mime_type=excluded.mime_type, byte_size=excluded.byte_size, sha256=excluded.sha256, width=excluded.width, height=excluded.height, duration_ms=excluded.duration_ms, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		item.ProfileID, item.MediaID, defaultString(item.Kind, "file"), nullEmpty(item.SourcePath), nullEmpty(item.ArchivePath), nullEmpty(item.MimeType), nullZero(item.ByteSize), nullEmpty(item.SHA256), nullZero(item.Width), nullZero(item.Height), nullZero(item.DurationMS), defaultString(item.RawJSON, "{}"), now)
	return err
}

func (a *Archive) UpsertFavorite(ctx context.Context, favorite Favorite) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into favorites(profile_id, favorite_id, kind, title, text, source_ref, raw_json, updated_at) values(?,?,?,?,?,?,?,?) on conflict(profile_id, favorite_id) do update set kind=excluded.kind, title=excluded.title, text=excluded.text, source_ref=excluded.source_ref, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		favorite.ProfileID, favorite.FavoriteID, defaultString(favorite.Kind, "unknown"), nullEmpty(favorite.Title), favorite.Text, nullEmpty(favorite.SourceRef), defaultString(favorite.RawJSON, "{}"), now)
	return err
}

func (a *Archive) UpsertArticle(ctx context.Context, article Article) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into biz_articles(profile_id, article_id, account_id, title, url, summary, published_at, raw_json, updated_at) values(?,?,?,?,?,?,?,?,?) on conflict(profile_id, article_id) do update set account_id=excluded.account_id, title=excluded.title, url=excluded.url, summary=excluded.summary, published_at=excluded.published_at, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		article.ProfileID, article.ArticleID, nullEmpty(article.AccountID), article.Title, nullEmpty(article.URL), article.Summary, nullEmpty(article.PublishedAt), defaultString(article.RawJSON, "{}"), now)
	if err != nil {
		return err
	}
	if _, err := a.store.DB().ExecContext(ctx, `delete from article_fts where rowid = (select rowid from biz_articles where profile_id=? and article_id=?)`, article.ProfileID, article.ArticleID); err != nil {
		return err
	}
	_, err = a.store.DB().ExecContext(ctx, `insert into article_fts(rowid, profile_id, article_id, account_id, body) values((select rowid from biz_articles where profile_id=? and article_id=?), ?, ?, ?, ?)`,
		article.ProfileID, article.ArticleID, article.ProfileID, article.ArticleID, nullEmpty(article.AccountID), NormalizeText(article.Title+" "+article.Summary))
	return err
}

func (a *Archive) UpsertMoment(ctx context.Context, moment Moment) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `insert into moments(profile_id, moment_id, author_id, text, created_at, raw_json, updated_at) values(?,?,?,?,?,?,?) on conflict(profile_id, moment_id) do update set author_id=excluded.author_id, text=excluded.text, created_at=excluded.created_at, raw_json=excluded.raw_json, updated_at=excluded.updated_at`,
		moment.ProfileID, moment.MomentID, nullEmpty(moment.AuthorID), moment.Text, nullEmpty(moment.CreatedAt), defaultString(moment.RawJSON, "{}"), now)
	return err
}

func (a *Archive) InsertRawRecord(ctx context.Context, profileID, sourceName, sourceTable, sourceKey, recordKind string, payload any) error {
	_, err := a.store.DB().ExecContext(ctx, `insert into raw_records(profile_id, source_name, source_table, source_key, record_kind, payload_json, observed_at) values(?,?,?,?,?,?,?)`,
		profileID, sourceName, sourceTable, sourceKey, defaultString(recordKind, "unsupported"), marshalRaw(payload), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (a *Archive) SearchMessages(ctx context.Context, query, chat, sender, kind, since string, limit int) ([]SearchHit, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	args := []any{ftsQuery(query)}
	clauses := []string{`message_fts match ?`}
	if strings.TrimSpace(chat) != "" {
		clauses = append(clauses, `m.chat_id = ?`)
		args = append(args, chat)
	}
	if strings.TrimSpace(sender) != "" {
		clauses = append(clauses, `coalesce(m.sender_id, '') = ?`)
		args = append(args, sender)
	}
	if strings.TrimSpace(kind) != "" {
		clauses = append(clauses, `m.message_type = ?`)
		args = append(args, kind)
	}
	if strings.TrimSpace(since) != "" {
		clauses = append(clauses, `coalesce(m.sent_at, '') >= ?`)
		args = append(args, since)
	}
	args = append(args, limit)
	rows, err := a.store.DB().QueryContext(ctx, `select m.profile_id, m.message_id, m.chat_id, coalesce(m.sender_id,''), coalesce(m.sent_at,''), m.message_type, m.text, bm25(message_fts) from message_fts join messages m on m.rowid = message_fts.rowid where `+strings.Join(clauses, " and ")+` order by bm25(message_fts) limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []SearchHit{}
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.ProfileID, &hit.MessageID, &hit.ChatID, &hit.SenderID, &hit.SentAt, &hit.Type, &hit.Text, &hit.Rank); err != nil {
			return nil, err
		}
		hit.Entity = "message"
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(chat) != "" || strings.TrimSpace(sender) != "" || strings.TrimSpace(kind) != "" {
		return hits, nil
	}
	articleHits, err := a.searchArticleHits(ctx, query, since, limit)
	if err != nil {
		return nil, err
	}
	hits = append(hits, articleHits...)
	otherHits, err := a.searchStructuredTextHits(ctx, query, since, limit)
	if err != nil {
		return nil, err
	}
	hits = append(hits, otherHits...)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (a *Archive) searchArticleHits(ctx context.Context, query, since string, limit int) ([]SearchHit, error) {
	args := []any{ftsQuery(query)}
	clauses := []string{`article_fts match ?`}
	if strings.TrimSpace(since) != "" {
		clauses = append(clauses, `coalesce(a.published_at, '') >= ?`)
		args = append(args, since)
	}
	args = append(args, limit)
	rows, err := a.store.DB().QueryContext(ctx, `select a.profile_id, a.article_id, coalesce(a.account_id,''), coalesce(a.published_at,''), trim(a.title || ' ' || a.summary), bm25(article_fts) from article_fts join biz_articles a on a.rowid = article_fts.rowid where `+strings.Join(clauses, " and ")+` order by bm25(article_fts) limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []SearchHit{}
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.ProfileID, &hit.ArticleID, &hit.SenderID, &hit.SentAt, &hit.Text, &hit.Rank); err != nil {
			return nil, err
		}
		hit.Entity = "article"
		hit.Type = "article"
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (a *Archive) searchStructuredTextHits(ctx context.Context, query, since string, limit int) ([]SearchHit, error) {
	pattern := "%" + query + "%"
	since = strings.TrimSpace(since)
	if since == "" {
		since = "0000"
	}
	rows, err := a.store.DB().QueryContext(ctx, `select 'message_part', p.profile_id, p.message_id, p.kind, coalesce(p.text,'') || ' ' || coalesce(p.url,''), coalesce(m.sent_at,'') from message_parts p left join messages m on m.profile_id = p.profile_id and m.message_id = p.message_id where (p.text like ? or coalesce(p.url,'') like ?) and coalesce(m.sent_at,'9999') >= ?
union all
select 'favorite', profile_id, favorite_id, kind, coalesce(title,'') || ' ' || text, coalesce(updated_at,'') from favorites where (coalesce(title,'') like ? or text like ?) and coalesce(updated_at,'9999') >= ?
union all
select 'moment', profile_id, moment_id, 'moment', text, coalesce(created_at,'') from moments where text like ? and coalesce(created_at,'9999') >= ?
union all
select 'contact', profile_id, contact_id, kind, trim(coalesce(display_name,'') || ' ' || coalesce(remark_name,'') || ' ' || coalesce(alias,'') || ' ' || contact_id), coalesce(updated_at,'') from contacts where (coalesce(display_name,'') like ? or coalesce(remark_name,'') like ? or coalesce(alias,'') like ? or contact_id like ?) and coalesce(updated_at,'9999') >= ?
union all
select 'chat', profile_id, chat_id, kind, trim(coalesce(title,'') || ' ' || chat_id), coalesce(last_message_at, updated_at, '') from chats where (coalesce(title,'') like ? or chat_id like ?) and coalesce(coalesce(last_message_at, updated_at), '9999') >= ?
union all
select 'media', profile_id, media_id, kind, trim(coalesce(source_path,'') || ' ' || coalesce(archive_path,'') || ' ' || coalesce(mime_type,'') || ' ' || media_id), coalesce(updated_at,'') from media_items where (coalesce(source_path,'') like ? or coalesce(archive_path,'') like ? or coalesce(mime_type,'') like ? or media_id like ?) and coalesce(updated_at,'9999') >= ?
limit ?`, pattern, pattern, since, pattern, pattern, since, pattern, since, pattern, pattern, pattern, pattern, since, pattern, pattern, since, pattern, pattern, pattern, pattern, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []SearchHit{}
	for rows.Next() {
		var entity, id string
		var hit SearchHit
		if err := rows.Scan(&entity, &hit.ProfileID, &id, &hit.Type, &hit.Text, &hit.SentAt); err != nil {
			return nil, err
		}
		hit.Entity = entity
		switch entity {
		case "message_part":
			hit.MessageID = id
		case "favorite":
			hit.FavoriteID = id
		case "moment":
			hit.MomentID = id
		case "contact":
			hit.ContactID = id
		case "chat":
			hit.ChatID = id
		case "media":
			hit.MediaID = id
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func ftsQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return query
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.IsSpace(r) {
			continue
		}
		return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	}
	return query
}

func (a *Archive) RebuildFTS(ctx context.Context) error {
	if _, err := a.store.DB().ExecContext(ctx, `delete from message_fts`); err != nil {
		return err
	}
	if _, err := a.store.DB().ExecContext(ctx, `insert into message_fts(rowid, profile_id, message_id, chat_id, sender_id, body) select rowid, profile_id, message_id, chat_id, sender_id, normalized_text from messages`); err != nil {
		return err
	}
	if _, err := a.store.DB().ExecContext(ctx, `delete from article_fts`); err != nil {
		return err
	}
	_, err := a.store.DB().ExecContext(ctx, `insert into article_fts(rowid, profile_id, article_id, account_id, body) select rowid, profile_id, article_id, account_id, trim(title || ' ' || summary) from biz_articles`)
	return err
}

func (a *Archive) List(ctx context.Context, table string, limit int) (ckstore.QueryResult, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	switch table {
	case "profiles", "contacts", "chats", "messages", "favorites", "biz_articles", "media_items", "sync_runs":
	default:
		return ckstore.QueryResult{}, fmt.Errorf("unsupported list table %q", table)
	}
	return a.store.Query(ctx, "select * from "+ckstore.QuoteIdent(table)+" limit ?", limit)
}

func (a *Archive) scalar(ctx context.Context, query string) (int64, error) {
	var n int64
	return n, a.store.DB().QueryRowContext(ctx, query).Scan(&n)
}

func (a *Archive) groupCounts(ctx context.Context, query string) (map[string]int64, error) {
	rows, err := a.store.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var key string
		var value int64
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func scanRun(rows interface{ Scan(...any) error }) (SyncRun, error) {
	var run SyncRun
	var warnings string
	err := rows.Scan(&run.RunID, &run.Source, &run.ProfileID, &run.StartedAt, &run.FinishedAt, &run.Status, &run.AppVersion, &run.SourceRoot, &run.SnapshotPath, &run.SourceDBCount, &run.ImportedProfiles, &run.ImportedContacts, &run.ImportedChats, &run.ImportedMessages, &run.ImportedMedia, &run.ImportedRawRecords, &warnings)
	if err != nil {
		return run, err
	}
	_ = json.Unmarshal([]byte(warnings), &run.Warnings)
	return run, nil
}

func marshalRaw(value any) string {
	if value == nil {
		return "{}"
	}
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return "{}"
		}
		return s
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nullEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func NormalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
