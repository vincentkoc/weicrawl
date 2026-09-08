package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/source/importer"
)

func nativeArchive(t *testing.T) *archive.Archive {
	t.Helper()
	arc, err := archive.Open(t.Context(), filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = arc.Close() })
	return arc
}

func nativeSourceSQL(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func nativeImport(t *testing.T, arc *archive.Archive, profile string, files []importer.File) importer.Result {
	t.Helper()
	result, warnings, err := importer.ImportFixtureDatabases(t.Context(), arc, profile, files)
	if err != nil {
		t.Fatalf("native import: %v; warnings=%v", err, warnings)
	}
	return result
}

func nativeQueryState(t *testing.T, arc *archive.Archive, queries ...string) string {
	t.Helper()
	var state []any
	for _, query := range queries {
		rows, err := arc.Query(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		state = append(state, rows.Values)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func legacyNativeMessage(shard string, localID int64) archive.Message {
	table := nativeMsgTable("alice")
	raw, _ := json.Marshal(map[string]any{
		"source_db": shard, "source_role": "message", "source_table": table, "local_id": localID,
	})
	rowID := table + ":" + strconv.FormatInt(localID, 10)
	return archive.Message{
		ProfileID: "fixture", MessageID: "message:" + rowID, ChatID: "alice",
		MessageType: "text", Text: "retained legacy", SourceDB: "message",
		SourceRowID: rowID, RawJSON: string(raw),
	}
}

func TestNativeShardIdentityStableAcrossOrderRootsAndProfiles(t *testing.T) {
	root := t.TempDir()
	files := []importer.File{
		{Path: filepath.Join(root, "message_0.db"), Role: "message"},
		{Path: filepath.Join(root, "message_1.db"), Role: "message"},
	}
	for _, file := range files {
		createNativeMessageDB(t, file.Path, "alice")
	}
	nativeSourceSQL(t, files[1].Path, `update "`+nativeMsgTable("alice")+`" set message_content='second shard' where local_id=7`)
	arc := nativeArchive(t)
	if result := nativeImport(t, arc, "fixture", files); result.Messages != 6 {
		t.Fatalf("observations=%d", result.Messages)
	}
	var count int
	if err := arc.DB().QueryRow("select count(*) from messages").Scan(&count); err != nil || count != 6 {
		t.Fatalf("canonical rows=%d error=%v", count, err)
	}
	stateQuery := `select profile_id,message_id,text,source_db,source_rowid,raw_json from messages order by profile_id,message_id`
	before := nativeQueryState(t, arc, stateQuery)
	if !strings.Contains(before, "native-v2:") || !strings.Contains(before, "second shard") {
		t.Fatal("new namespace or second shard missing")
	}
	nativeImport(t, arc, "fixture", []importer.File{files[1], files[0]})
	if after := nativeQueryState(t, arc, stateQuery); after != before {
		t.Fatal("reversed-order reimport changed identities")
	}
	moved := t.TempDir()
	for i := range files {
		data, err := os.ReadFile(files[i].Path)
		if err != nil {
			t.Fatal(err)
		}
		files[i].Path = filepath.Join(moved, filepath.Base(files[i].Path))
		if err := os.WriteFile(files[i].Path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nativeImport(t, arc, "fixture", files)
	if after := nativeQueryState(t, arc, stateQuery); after != before {
		t.Fatal("moving only the source parent changed identities")
	}
	nativeImport(t, arc, "other-profile", files)
	if err := arc.DB().QueryRow("select count(*) from messages").Scan(&count); err != nil || count != 12 {
		t.Fatalf("profiles merged: count=%d error=%v", count, err)
	}
}

func TestNativeLegacyIdentityKeepsReferences(t *testing.T) {
	root := t.TempDir()
	files := []importer.File{
		{Path: filepath.Join(root, "message_0.db"), Role: "message"},
		{Path: filepath.Join(root, "message_1.db"), Role: "message"},
	}
	for _, file := range files {
		createNativeMessageDB(t, file.Path, "alice")
	}
	arc := nativeArchive(t)
	legacy := legacyNativeMessage("message_0.db", 7)
	if err := arc.UpsertMessage(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := arc.UpsertMessage(t.Context(), archive.Message{
		ProfileID: "fixture", MessageID: "explicit-fixture", ChatID: "alice",
		Text: "unrelated canonical fixture", SourceDB: "fixture", SourceRowID: "explicit-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if err := arc.UpsertMessagePart(t.Context(), archive.MessagePart{
		ProfileID: "fixture", MessageID: legacy.MessageID, PartIndex: 99, Kind: "image", MediaID: "legacy-media",
	}); err != nil {
		t.Fatal(err)
	}
	if err := arc.InsertMessageEvent(t.Context(), archive.MessageEvent{
		ProfileID: "fixture", MessageID: legacy.MessageID, ChatID: "alice",
		EventType: "legacy", EventAt: "2026-09-08T00:00:00Z", PayloadJSON: `{"retained":true}`,
	}); err != nil {
		t.Fatal(err)
	}
	reference, _ := json.Marshal(map[string]string{"message_id": legacy.MessageID})
	if err := arc.UpsertArticle(t.Context(), archive.Article{
		ProfileID: "fixture", ArticleID: "legacy-article", AccountID: "alice", RawJSON: string(reference),
	}); err != nil {
		t.Fatal(err)
	}
	if err := arc.UpsertMedia(t.Context(), archive.MediaItem{
		ProfileID: "fixture", MediaID: "legacy-media", RawJSON: string(reference),
	}); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"select * from message_parts where part_index=99",
		"select * from message_events where event_type='legacy'",
		"select * from biz_articles where article_id='legacy-article'",
		"select * from media_items where media_id='legacy-media'",
		"select * from messages where message_id='explicit-fixture'",
	}
	before := nativeQueryState(t, arc, queries...)
	nativeImport(t, arc, "fixture", files)
	nativeImport(t, arc, "fixture", []importer.File{files[1], files[0]})
	if after := nativeQueryState(t, arc, queries...); after != before {
		t.Fatal("legacy references or unrelated fixture changed")
	}
	var count int
	if err := arc.DB().QueryRow("select count(*) from messages").Scan(&count); err != nil || count != 7 {
		t.Fatalf("legacy rows duplicated or lost: %d %v", count, err)
	}
	if err := arc.DB().QueryRow("select count(*) from message_fts where profile_id=? and message_id=?", "fixture", legacy.MessageID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy FTS identity lost: %d %v", count, err)
	}
}

func TestNativeIdentityRejectsAmbiguousTargetsBeforeMessageWrites(t *testing.T) {
	for _, scenario := range []string{
		"new-missing-provenance", "new-version", "new-shard", "new-role-column",
		"new-row-column", "new-fractional-id", "new-missing-version", "legacy-missing-provenance",
		"legacy-role-column", "legacy-row-column", "legacy-version", "legacy-fractional-id",
		"matching-legacy-and-new",
	} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "message_0.db")
			createNativeMessageDB(t, path, "alice")
			files := []importer.File{{Path: path, Role: "message"}}
			arc := nativeArchive(t)
			nativeImport(t, arc, "fixture", files)
			legacy := legacyNativeMessage("message_0.db", 9)
			var id string
			if err := arc.DB().QueryRow("select message_id from messages where source_rowid=?", legacy.SourceRowID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(scenario, "legacy-") || scenario == "matching-legacy-and-new" {
				if scenario == "legacy-missing-provenance" {
					legacy.RawJSON = "{}"
				}
				if scenario == "legacy-role-column" {
					legacy.SourceDB = "fixture"
				}
				if scenario == "legacy-row-column" {
					legacy.SourceRowID = "unrelated"
				}
				if scenario == "legacy-version" {
					legacy.RawJSON = strings.Replace(legacy.RawJSON, "{", `{"native_identity_version":3,`, 1)
				}
				if scenario == "legacy-fractional-id" {
					legacy.RawJSON = strings.Replace(legacy.RawJSON, `"local_id":9`, `"local_id":9.5`, 1)
				}
				if err := arc.UpsertMessage(t.Context(), legacy); err != nil {
					t.Fatal(err)
				}
			} else {
				column, value := "raw_json", "{}"
				switch scenario {
				case "new-version":
					value = `{"native_identity_version":3,"source_db":"message_0.db","source_role":"message","source_table":"` + nativeMsgTable("alice") + `","local_id":9}`
				case "new-shard":
					value = `{"native_identity_version":2,"source_db":"other.db","source_role":"message","source_table":"` + nativeMsgTable("alice") + `","local_id":9}`
				case "new-fractional-id":
					value = `{"native_identity_version":2,"source_db":"message_0.db","source_role":"message","source_table":"` + nativeMsgTable("alice") + `","local_id":9.5}`
				case "new-missing-version":
					value = legacy.RawJSON
				case "new-role-column":
					column, value = "source_db", "fixture"
				case "new-row-column":
					column, value = "source_rowid", "unrelated"
				}
				if _, err := arc.DB().Exec("update messages set "+column+"=? where message_id=?", value, id); err != nil {
					t.Fatal(err)
				}
			}
			queries := []string{
				"select * from messages order by message_id",
				"select * from message_parts order by message_id,part_index",
				"select * from message_events order by event_id",
				"select * from message_fts order by rowid",
				"select * from biz_articles order by article_id",
				"select * from media_items order by media_id",
			}
			before := nativeQueryState(t, arc, queries...)
			nativeSourceSQL(t, path, `update "`+nativeMsgTable("alice")+`" set message_content='changed input'`)
			if _, _, err := importer.ImportFixtureDatabases(t.Context(), arc, "fixture", files); err == nil {
				t.Fatal("ambiguous target accepted")
			}
			if after := nativeQueryState(t, arc, queries...); after != before {
				t.Fatal("preflight changed the affected messages or dependents")
			}
		})
	}
}

func TestNativeIdentityUsesExactLargeLocalIDs(t *testing.T) {
	const large int64 = 9007199254740993
	path := filepath.Join(t.TempDir(), "message_0.db")
	createNativeMessageDB(t, path, "alice")
	nativeSourceSQL(t, path, `update "`+nativeMsgTable("alice")+`" set local_id=? where local_id=7`, large)
	nativeSourceSQL(t, path, `update "`+nativeMsgTable("alice")+`" set local_id=? where local_id=8`, large-1)
	arc := nativeArchive(t)
	legacy := legacyNativeMessage("message_0.db", large)
	if err := arc.UpsertMessage(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	files := []importer.File{{Path: path, Role: "message"}}
	nativeImport(t, arc, "fixture", files)
	nativeImport(t, arc, "fixture", files)
	var count int
	if err := arc.DB().QueryRow("select count(*) from messages").Scan(&count); err != nil || count != 3 {
		t.Fatalf("large IDs merged or duplicated: %d %v", count, err)
	}
	var raw string
	if err := arc.DB().QueryRow("select raw_json from messages where message_id=?", legacy.MessageID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var identity struct {
		LocalID int64 `json:"local_id"`
	}
	if err := json.Unmarshal([]byte(raw), &identity); err != nil || identity.LocalID != large {
		t.Fatalf("large legacy local_id rounded: %d %v", identity.LocalID, err)
	}
}

func TestNativeSourceNamespacePreflightUsesSameFile(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first", "message_0.db")
	createNativeMessageDB(t, first, "alice")
	alias := filepath.Join(root, "alias", "message_0.db")
	if err := os.Mkdir(filepath.Dir(alias), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, alias); err != nil {
		t.Fatal(err)
	}
	arc := nativeArchive(t)
	result := nativeImport(t, arc, "fixture", []importer.File{{Path: first, Role: "message"}, {Path: alias, Role: "message"}})
	if result.Messages != 3 {
		t.Fatalf("same inode imported twice: %d", result.Messages)
	}
	symlink := filepath.Join(root, "symlink", "message_0.db")
	if err := os.Mkdir(filepath.Dir(symlink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, symlink); err != nil {
		t.Fatal(err)
	}
	result = nativeImport(t, arc, "fixture", []importer.File{{Path: first, Role: "message"}, {Path: symlink, Role: "message"}})
	if result.Messages != 3 {
		t.Fatalf("symlink alias imported twice: %d", result.Messages)
	}
	distinct := filepath.Join(root, "distinct", "message_0.db")
	if err := os.Mkdir(filepath.Dir(distinct), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(distinct, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before := nativeQueryState(t, arc, "select * from messages order by message_id")
	if _, _, err := importer.ImportFixtureDatabases(t.Context(), arc, "fixture",
		[]importer.File{{Path: first, Role: "message"}, {Path: distinct, Role: "message"}}); err == nil {
		t.Fatal("different inodes with identical contents shared a namespace")
	}
	if after := nativeQueryState(t, arc, "select * from messages order by message_id"); after != before {
		t.Fatal("duplicate namespace preflight changed messages")
	}
}
