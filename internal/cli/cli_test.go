package cli

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCLIEndToEndWithSyntheticDesktopFixture(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "config")
	cacheRoot := filepath.Join(root, "cache")
	cfgPath := filepath.Join(configRoot, "weicrawl", "config.toml")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("WEICRAWL_CACHE_DIR", cacheRoot)
	t.Setenv("WEICRAWL_CONFIG", cfgPath)

	container := filepath.Join(root, "WeChatContainer")
	profileRoot := filepath.Join(container, "Data", "Documents", "xwechat_files", "wxid_fixture_abcd")
	messageDir := filepath.Join(profileRoot, "db_storage", "message")
	if err := os.MkdirAll(messageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createFixtureDB(t, filepath.Join(messageDir, "message_0.db"))
	mediaPath := filepath.Join(profileRoot, "msg", "file", "2026-06", "sample.txt")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("sample media"), 0o600); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(configRoot, "weicrawl", "weicrawl.db")
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	patchConfigPath(t, cfgPath, container)

	code, out, errOut = runForTest("--json", "doctor")
	if code != 0 {
		t.Fatalf("doctor code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var doctor map[string]any
	if err := json.Unmarshal(out.Bytes(), &doctor); err != nil {
		t.Fatal(err)
	}
	if desktop := doctor["desktop_macos"].(map[string]any); int(desktop["database_count"].(float64)) != 1 {
		t.Fatalf("doctor desktop = %#v", desktop)
	}

	code, out, errOut = runForTest("--json", "sync", "--profile", "wxid_fixture", "--include-media")
	if code != 0 {
		t.Fatalf("sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var sync map[string]any
	if err := json.Unmarshal(out.Bytes(), &sync); err != nil {
		t.Fatal(err)
	}
	if got := int(sync["imported_messages"].(float64)); got != 2 {
		t.Fatalf("imported_messages = %d, sync=%#v", got, sync)
	}
	if got := int(sync["imported_media"].(float64)); got != 1 {
		t.Fatalf("imported_media = %d, sync=%#v", got, sync)
	}

	code, out, errOut = runForTest("--json", "status")
	if code != 0 {
		t.Fatalf("status code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	archiveStatus := status["archive"].(map[string]any)
	if got := int(archiveStatus["message_count"].(float64)); got != 2 {
		t.Fatalf("message_count = %d, status=%#v", got, archiveStatus)
	}
	if got := int(archiveStatus["media_metadata_count"].(float64)); got != 1 {
		t.Fatalf("media_metadata_count = %d, status=%#v", got, archiveStatus)
	}

	code, out, errOut = runForTest("--json", "search", "航班")
	if code != 0 {
		t.Fatalf("search code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var search map[string]any
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) != 1 {
		t.Fatalf("hits = %#v", hits)
	}

	markdownDir := filepath.Join(root, "markdown")
	code, out, errOut = runForTest("--json", "export", "--format", "markdown", "--out", markdownDir)
	if code != 0 {
		t.Fatalf("export markdown code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if _, err := os.Stat(filepath.Join(markdownDir, "chat-1.md")); err != nil {
		t.Fatalf("markdown export missing: %v", err)
	}

	snapshotDir := filepath.Join(root, "snapshot")
	code, out, errOut = runForTest("--json", "snapshot", "create", "--out", snapshotDir)
	if code != 0 {
		t.Fatalf("snapshot create code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if _, err := os.Stat(filepath.Join(snapshotDir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	importDB := filepath.Join(root, "imported.db")
	code, out, errOut = runForTest("--json", "--db", importDB, "import", snapshotDir)
	if code != 0 {
		t.Fatalf("import code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	db, err := sql.Open("sqlite", importDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var messages int
	if err := db.QueryRowContext(context.Background(), `select count(*) from messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 2 {
		t.Fatalf("imported messages = %d", messages)
	}
	_ = dbPath
}

func TestUnlockDesktopDoesNotClaimAvailability(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	code, out, errOut := runForTest("--json", "unlock", "desktop", "--allow-process-inspect", "--explain")
	if code != 0 {
		t.Fatalf("unlock code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if available := payload["available"].(bool); available {
		t.Fatalf("unlock unexpectedly available: %#v", payload)
	}
	if _, ok := payload["warning"]; !ok {
		t.Fatalf("unlock warning missing: %#v", payload)
	}
}

func TestOfficialAccountSyncSkipsWithoutCredentials(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("WEICRAWL_WECHAT_APP_ID", "")
	t.Setenv("WEICRAWL_WECHAT_APP_SECRET", "")
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "sync", "--source", "official-account-api")
	if code != 0 {
		t.Fatalf("sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "skipped" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestCLIImportsNativeReadableWeChatShape(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	cfgPath := filepath.Join(configRoot, "weicrawl", "config.toml")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("WEICRAWL_CACHE_DIR", filepath.Join(root, "cache"))
	t.Setenv("WEICRAWL_CONFIG", cfgPath)

	container := filepath.Join(root, "WeChatContainer")
	profileRoot := filepath.Join(container, "Data", "Documents", "xwechat_files", "wxid_native_abcd")
	createNativeContactDB(t, filepath.Join(profileRoot, "db_storage", "contact", "contact.db"))
	createNativeSessionDB(t, filepath.Join(profileRoot, "db_storage", "session", "session.db"))
	createNativeMessageDB(t, filepath.Join(profileRoot, "db_storage", "message", "message_0.db"), "alice")

	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	patchConfigPath(t, cfgPath, container)
	code, out, errOut = runForTest("--json", "sync", "--profile", "wxid_native")
	if code != 0 {
		t.Fatalf("sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := int(payload["imported_contacts"].(float64)); got != 1 {
		t.Fatalf("imported_contacts = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_messages"].(float64)); got != 1 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
	code, out, errOut = runForTest("--json", "search", "native")
	if code != 0 {
		t.Fatalf("search code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var search map[string]any
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) != 1 {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestCLISyncDecryptedDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	decrypted := filepath.Join(root, "decrypted")
	createNativeMessageDB(t, filepath.Join(decrypted, "message", "message_0.db"), "alice")
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "sync", "--source", "desktop-macos", "--profile", "profile-decrypted", "--decrypted-dir", decrypted)
	if code != 0 {
		t.Fatalf("sync decrypted code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["source"] != "desktop-macos-decrypted" {
		t.Fatalf("payload = %#v", payload)
	}
	if got := int(payload["imported_messages"].(float64)); got != 1 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
}

func runForTest(args ...string) (int, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	return code, &stdout, &stderr
}

func createNativeContactDB(t *testing.T, path string) {
	t.Helper()
	db := openFixtureDB(t, path)
	defer db.Close()
	_, err := db.Exec(`create table contact(username text, remark text, nick_name text, type text);
create table stranger(username text, remark text, nick_name text, type text);
insert into contact values('alice', 'Alice Remark', 'Alice Nick', '');`)
	if err != nil {
		t.Fatal(err)
	}
}

func createNativeSessionDB(t *testing.T, path string) {
	t.Helper()
	db := openFixtureDB(t, path)
	defer db.Close()
	_, err := db.Exec(`create table SessionTable(username text, type integer, summary text, last_sender_display_name text, last_timestamp integer);
insert into SessionTable values('alice', 0, 'native hello', 'Alice', 1781323200);`)
	if err != nil {
		t.Fatal(err)
	}
}

func createNativeMessageDB(t *testing.T, path, username string) {
	t.Helper()
	db := openFixtureDB(t, path)
	defer db.Close()
	table := nativeMsgTable(username)
	_, err := db.Exec(`create table Name2Id(user_name text);
insert into Name2Id values(?);`, username)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table "` + table + `"(local_id integer, local_type integer, create_time integer, real_sender_id text, message_content text, source text);
insert into "` + table + `" values(7, 1, 1781323200, 'alice', 'native hello from decrypted shape', '0');`)
	if err != nil {
		t.Fatal(err)
	}
}

func openFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func nativeMsgTable(username string) string {
	sum := md5.Sum([]byte(username))
	return "Msg_" + hex.EncodeToString(sum[:])
}

func createFixtureDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := `
create table weicrawl_fixture_contacts(contact_id text, alias text, display_name text, remark_name text, kind text, avatar_ref text, raw_json text);
create table weicrawl_fixture_chats(chat_id text, kind text, title text, last_message_at text, unread_count integer, muted integer, pinned integer, raw_json text);
create table weicrawl_fixture_messages(message_id text, chat_id text, sender_id text, direction text, message_type text, sent_at text, text text, normalized_text text, source_rowid text, raw_json text);
insert into weicrawl_fixture_contacts values('alice', 'alice', 'Alice', '', 'user', '', '{}');
insert into weicrawl_fixture_chats values('chat-1', 'direct', 'Alice', '2026-06-13T01:00:00Z', 0, 0, 0, '{}');
insert into weicrawl_fixture_messages values('m1', 'chat-1', 'alice', 'inbound', 'text', '2026-06-13T01:00:00Z', 'hello from fixture', 'hello from fixture', '1', '{}');
insert into weicrawl_fixture_messages values('m2', 'chat-1', 'alice', 'inbound', 'text', '2026-06-13T02:00:00Z', '航班 changed', '航班 changed', '2', '{}');
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
}

func patchConfigPath(t *testing.T, path, container string) {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bytes)
	body = strings.ReplaceAll(body, `container_path = '~/Library/Containers/com.tencent.xinWeChat'`, `container_path = '`+container+`'`)
	body = strings.ReplaceAll(body, `container_path = "~/Library/Containers/com.tencent.xinWeChat"`, `container_path = "`+container+`"`)
	if !strings.Contains(body, container) {
		t.Fatalf("failed to patch container path in config:\n%s", body)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
