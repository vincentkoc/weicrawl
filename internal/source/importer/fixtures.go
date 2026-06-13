package importer

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/vincentkoc/weicrawl/internal/archive"
)

type Result struct {
	Contacts int64 `json:"contacts"`
	Chats    int64 `json:"chats"`
	Messages int64 `json:"messages"`
	Media    int64 `json:"media"`
}

type File struct {
	Path string
	Role string
}

func ImportFixtureDatabases(ctx context.Context, arc *archive.Archive, profileID string, files []File) (Result, []string, error) {
	var result Result
	var warnings []string
	for _, file := range files {
		src, err := ckstore.OpenReadOnly(ctx, file.Path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: open readonly failed; likely encrypted or not sqlite", file.Role))
			continue
		}
		counts, err := importReadableDB(ctx, arc, src.DB(), profileID, file)
		_ = src.Close()
		result.Contacts += counts.Contacts
		result.Chats += counts.Chats
		result.Messages += counts.Messages
		result.Media += counts.Media
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", file.Role, err))
		}
	}
	return result, warnings, nil
}

func importReadableDB(ctx context.Context, arc *archive.Archive, db *sql.DB, profileID string, file File) (Result, error) {
	var total Result
	fixture, err := importFixtureDB(ctx, arc, db, profileID, file)
	if err != nil {
		return total, err
	}
	total.add(fixture)
	native, err := importNativeDB(ctx, arc, db, profileID, file)
	if err != nil {
		return total, err
	}
	total.add(native)
	return total, nil
}

func importFixtureDB(ctx context.Context, arc *archive.Archive, db *sql.DB, profileID string, file File) (Result, error) {
	var result Result
	tables, err := tableSet(ctx, db)
	if err != nil {
		return result, err
	}
	if tables["weicrawl_fixture_contacts"] {
		rows, err := db.QueryContext(ctx, `select contact_id, coalesce(alias,''), coalesce(display_name,''), coalesce(remark_name,''), coalesce(kind,'user'), coalesce(avatar_ref,''), coalesce(raw_json,'{}') from weicrawl_fixture_contacts`)
		if err != nil {
			return result, err
		}
		defer rows.Close()
		for rows.Next() {
			var contactID, alias, displayName, remarkName, kind, avatarRef, raw string
			if err := rows.Scan(&contactID, &alias, &displayName, &remarkName, &kind, &avatarRef, &raw); err != nil {
				return result, err
			}
			if err := arc.UpsertContact(ctx, profileID, contactID, alias, displayName, remarkName, kind, avatarRef, raw); err != nil {
				return result, err
			}
			result.Contacts++
		}
	}
	if tables["weicrawl_fixture_chats"] {
		rows, err := db.QueryContext(ctx, `select chat_id, coalesce(kind,'unknown'), coalesce(title,''), coalesce(last_message_at,''), coalesce(unread_count,0), coalesce(muted,0), coalesce(pinned,0), coalesce(raw_json,'{}') from weicrawl_fixture_chats`)
		if err != nil {
			return result, err
		}
		defer rows.Close()
		for rows.Next() {
			var chatID, kind, title, lastMessageAt, raw string
			var unread int64
			var muted, pinned int
			if err := rows.Scan(&chatID, &kind, &title, &lastMessageAt, &unread, &muted, &pinned, &raw); err != nil {
				return result, err
			}
			if err := arc.UpsertChat(ctx, profileID, chatID, kind, title, lastMessageAt, unread, muted != 0, pinned != 0, raw); err != nil {
				return result, err
			}
			result.Chats++
		}
	}
	if tables["weicrawl_fixture_messages"] {
		rows, err := db.QueryContext(ctx, `select message_id, chat_id, coalesce(sender_id,''), coalesce(direction,'unknown'), coalesce(message_type,'unsupported'), coalesce(sent_at,''), coalesce(text,''), coalesce(normalized_text,''), coalesce(source_rowid,message_id), coalesce(raw_json,'{}') from weicrawl_fixture_messages`)
		if err != nil {
			return result, err
		}
		defer rows.Close()
		for rows.Next() {
			var msg archive.Message
			msg.ProfileID = profileID
			msg.SourceDB = file.Role
			if err := rows.Scan(&msg.MessageID, &msg.ChatID, &msg.SenderID, &msg.Direction, &msg.MessageType, &msg.SentAt, &msg.Text, &msg.NormalizedText, &msg.SourceRowID, &msg.RawJSON); err != nil {
				return result, err
			}
			if strings.TrimSpace(msg.ChatID) == "" {
				msg.ChatID = "unknown"
			}
			if err := arc.UpsertMessage(ctx, msg); err != nil {
				return result, err
			}
			result.Messages++
		}
	}
	return result, nil
}

func importNativeDB(ctx context.Context, arc *archive.Archive, db *sql.DB, profileID string, file File) (Result, error) {
	var result Result
	tables, err := tableSet(ctx, db)
	if err != nil {
		return result, err
	}
	columns, err := allTableColumns(ctx, db)
	if err != nil {
		return result, err
	}
	if tables["contact"] && hasColumns(columns["contact"], "username") {
		n, err := importContactTable(ctx, arc, db, profileID, "contact", "user")
		if err != nil {
			return result, err
		}
		result.Contacts += n
	}
	if tables["stranger"] && hasColumns(columns["stranger"], "username") {
		n, err := importContactTable(ctx, arc, db, profileID, "stranger", "user")
		if err != nil {
			return result, err
		}
		result.Contacts += n
	}
	if tables["SessionTable"] && hasColumns(columns["SessionTable"], "username") {
		n, err := importSessionTable(ctx, arc, db, profileID)
		if err != nil {
			return result, err
		}
		result.Chats += n
	}
	if tables["Name2Id"] && hasColumns(columns["Name2Id"], "user_name") {
		counts, err := importMessageTables(ctx, arc, db, profileID, file, tables)
		if err != nil {
			return result, err
		}
		result.add(counts)
	}
	return result, nil
}

func importContactTable(ctx context.Context, arc *archive.Archive, db *sql.DB, profileID, table, fallbackKind string) (int64, error) {
	rows, err := db.QueryContext(ctx, `select username, coalesce(remark,''), coalesce(nick_name,''), coalesce(type,''), json_object('source_table', ?, 'username', username, 'remark', coalesce(remark,''), 'nick_name', coalesce(nick_name,'')) from `+quoteIdent(table)+` where coalesce(username,'') <> ''`, table)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var username, remark, nick, typeValue, raw string
		if err := rows.Scan(&username, &remark, &nick, &typeValue, &raw); err != nil {
			return n, err
		}
		kind := fallbackKind
		if strings.Contains(username, "@chatroom") {
			kind = "group"
		} else if strings.HasPrefix(username, "gh_") || strings.Contains(username, "@app") {
			kind = "public_account"
		}
		display := firstNonEmpty(remark, nick, username)
		if err := arc.UpsertContact(ctx, profileID, username, username, display, remark, kind, "", raw); err != nil {
			return n, err
		}
		_ = typeValue
		n++
	}
	return n, rows.Err()
}

func importSessionTable(ctx context.Context, arc *archive.Archive, db *sql.DB, profileID string) (int64, error) {
	rows, err := db.QueryContext(ctx, `select username, coalesce(type,0), coalesce(summary,''), coalesce(last_sender_display_name,''), coalesce(last_timestamp,0), json_object('source_table','SessionTable','username',username,'summary',coalesce(summary,''),'last_sender_display_name',coalesce(last_sender_display_name,''),'last_timestamp',coalesce(last_timestamp,0)) from SessionTable where coalesce(username,'') <> ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var username, summary, sender, raw string
		var sessionType, ts int64
		if err := rows.Scan(&username, &sessionType, &summary, &sender, &ts, &raw); err != nil {
			return n, err
		}
		kind := "direct"
		if strings.Contains(username, "@chatroom") {
			kind = "group"
		} else if strings.HasPrefix(username, "gh_") {
			kind = "public_account"
		}
		if sessionType == 10000 {
			kind = "system"
		}
		if err := arc.UpsertChat(ctx, profileID, username, kind, username, unixSeconds(ts), 0, false, false, raw); err != nil {
			return n, err
		}
		_ = summary
		_ = sender
		n++
	}
	return n, rows.Err()
}

func importMessageTables(ctx context.Context, arc *archive.Archive, db *sql.DB, profileID string, file File, tables map[string]bool) (Result, error) {
	var result Result
	rows, err := db.QueryContext(ctx, `select user_name from Name2Id where coalesce(user_name,'') <> ''`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	var usernames []string
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return result, err
		}
		usernames = append(usernames, username)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	for _, username := range usernames {
		table := messageTableName(username)
		if !tables[table] {
			continue
		}
		kind := "direct"
		if strings.Contains(username, "@chatroom") {
			kind = "group"
		} else if strings.HasPrefix(username, "gh_") {
			kind = "public_account"
		}
		if err := arc.UpsertChat(ctx, profileID, username, kind, username, "", 0, false, false, map[string]any{"source": "Name2Id", "db": filepath.Base(file.Path)}); err != nil {
			return result, err
		}
		result.Chats++
		query := `select local_id, local_type, coalesce(create_time,0), coalesce(real_sender_id,''), coalesce(message_content,''), coalesce(source,'') from ` + quoteIdent(table) + ` order by create_time asc, local_id asc`
		msgRows, err := db.QueryContext(ctx, query)
		if err != nil {
			return result, err
		}
		for msgRows.Next() {
			var localID, localType, createdAt int64
			var realSender, content, sourceValue string
			if err := msgRows.Scan(&localID, &localType, &createdAt, &realSender, &content, &sourceValue); err != nil {
				_ = msgRows.Close()
				return result, err
			}
			sender := realSender
			text := content
			if strings.Contains(username, "@chatroom") && strings.Contains(content, ":\n") {
				parts := strings.SplitN(content, ":\n", 2)
				sender = parts[0]
				text = parts[1]
			}
			if sender == "" {
				sender = username
			}
			raw := map[string]any{
				"source_db":    filepath.Base(file.Path),
				"source_role":  file.Role,
				"source_table": table,
				"local_id":     localID,
				"local_type":   localType,
				"source":       sourceValue,
			}
			rawJSON, _ := json.Marshal(raw)
			msg := archive.Message{
				ProfileID:      profileID,
				MessageID:      file.Role + ":" + table + ":" + strconv.FormatInt(localID, 10),
				ChatID:         username,
				SenderID:       sender,
				Direction:      directionFromSource(sourceValue),
				MessageType:    normalizeMessageType(localType),
				SentAt:         unixSeconds(createdAt),
				Text:           text,
				NormalizedText: archive.NormalizeText(text),
				SourceDB:       file.Role,
				SourceRowID:    table + ":" + strconv.FormatInt(localID, 10),
				RawJSON:        string(rawJSON),
			}
			if msg.MessageType != "text" && msg.Text == "" {
				msg.Text = "[" + msg.MessageType + "]"
				msg.NormalizedText = msg.Text
			}
			if err := arc.UpsertMessage(ctx, msg); err != nil {
				_ = msgRows.Close()
				return result, err
			}
			result.Messages++
		}
		if err := msgRows.Close(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func tableSet(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `select name from sqlite_master where type in ('table','view')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func allTableColumns(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	tables, err := tableSet(ctx, db)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for table := range tables {
		rows, err := db.QueryContext(ctx, `pragma table_info(`+quoteIdent(table)+`)`)
		if err != nil {
			continue
		}
		cols := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull int
			var defaultValue any
			var pk int
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				_ = rows.Close()
				return nil, err
			}
			cols[name] = true
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		out[table] = cols
	}
	return out, nil
}

func hasColumns(cols map[string]bool, names ...string) bool {
	for _, name := range names {
		if !cols[name] {
			return false
		}
	}
	return true
}

func messageTableName(username string) string {
	sum := md5.Sum([]byte(username))
	return "Msg_" + hex.EncodeToString(sum[:])
}

func normalizeMessageType(localType int64) string {
	switch localType {
	case 1:
		return "text"
	case 3:
		return "image"
	case 34:
		return "voice"
	case 42:
		return "card"
	case 43:
		return "video"
	case 47:
		return "sticker"
	case 48:
		return "location"
	case 49:
		return "link"
	case 10000, 10002:
		return "system"
	default:
		return "unsupported"
	}
}

func directionFromSource(source string) string {
	switch strings.TrimSpace(source) {
	case "1":
		return "outbound"
	case "0":
		return "inbound"
	default:
		return "unknown"
	}
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

func (r *Result) add(other Result) {
	r.Contacts += other.Contacts
	r.Chats += other.Chats
	r.Messages += other.Messages
	r.Media += other.Media
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
