package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	if check := doctorCheck(t, doctor, "fts_health"); !check["ok"].(bool) {
		t.Fatalf("fts check = %#v", check)
	}
	if check := doctorCheck(t, doctor, "crawlkit_metadata_valid"); !check["ok"].(bool) {
		t.Fatalf("metadata check = %#v", check)
	}
	if check := doctorCheck(t, doctor, "source_db_encryption_probe"); int(check["encrypted_count"].(float64)) != 0 {
		t.Fatalf("encryption check = %#v", check)
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
	if got := int(sync["imported_message_parts"].(float64)); got != 1 {
		t.Fatalf("imported_message_parts = %d, sync=%#v", got, sync)
	}
	if got := int(sync["imported_message_events"].(float64)); got != 1 {
		t.Fatalf("imported_message_events = %d, sync=%#v", got, sync)
	}
	if got := int(sync["imported_favorites"].(float64)); got != 1 {
		t.Fatalf("imported_favorites = %d, sync=%#v", got, sync)
	}
	if got := int(sync["imported_moments"].(float64)); got != 1 {
		t.Fatalf("imported_moments = %d, sync=%#v", got, sync)
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
	if got := int(archiveStatus["favorite_count"].(float64)); got != 1 {
		t.Fatalf("favorite_count = %d, status=%#v", got, archiveStatus)
	}
	if got := int(archiveStatus["moment_count"].(float64)); got != 1 {
		t.Fatalf("moment_count = %d, status=%#v", got, archiveStatus)
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
	code, out, errOut = runForTest("--json", "search", "--since", "2026-06-13T01:30:00Z", "hello")
	if code != 0 {
		t.Fatalf("search since code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) != 0 {
		t.Fatalf("since hits = %#v", hits)
	}
	code, out, errOut = runForTest("--json", "search", "boarding")
	if code != 0 {
		t.Fatalf("search boarding code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) < 2 {
		t.Fatalf("boarding hits = %#v", hits)
	}
	code, out, errOut = runForTest("--json", "tui", "--scope", "all", "--limit", "20")
	if code != 0 {
		t.Fatalf("tui json code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	tuiKinds := readTUIKindCounts(t, out.Bytes())
	for prefix, want := range map[string]int{"message:": 2, "favorite:": 1, "media:": 1, "moment": 1} {
		if tuiKinds[prefix] != want {
			t.Fatalf("tui kind %s = %d, counts=%#v", prefix, tuiKinds[prefix], tuiKinds)
		}
	}

	markdownDir := filepath.Join(root, "markdown")
	code, out, errOut = runForTest("--json", "export", "--format", "markdown", "--out", markdownDir)
	if code != 0 {
		t.Fatalf("export markdown code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if _, err := os.Stat(filepath.Join(markdownDir, "chat-1.md")); err != nil {
		t.Fatalf("markdown export missing: %v", err)
	}
	jsonlPath := filepath.Join(root, "archive.jsonl")
	code, out, errOut = runForTest("--json", "export", "--format", "jsonl", "--out", jsonlPath)
	if code != 0 {
		t.Fatalf("export jsonl code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	counts := readJSONLEntityCounts(t, jsonlPath)
	for entity, want := range map[string]int{
		"profile":       1,
		"contact":       1,
		"chat":          1,
		"message":       2,
		"message_part":  1,
		"message_event": 1,
		"favorite":      1,
		"media":         1,
		"moment":        1,
	} {
		if counts[entity] != want {
			t.Fatalf("jsonl %s count = %d, counts=%#v", entity, counts[entity], counts)
		}
	}
	messagesOnlyPath := filepath.Join(root, "messages.jsonl")
	code, out, errOut = runForTest("--json", "export", "--format", "jsonl", "--scope", "messages", "--out", messagesOnlyPath)
	if code != 0 {
		t.Fatalf("export messages jsonl code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	counts = readJSONLEntityCounts(t, messagesOnlyPath)
	if counts["message"] != 2 || counts["favorite"] != 0 {
		t.Fatalf("messages scope counts=%#v", counts)
	}
	jsonlImportDB := filepath.Join(root, "jsonl-imported.db")
	code, out, errOut = runForTest("--json", "--db", jsonlImportDB, "import", "--format", "jsonl", jsonlPath)
	if code != 0 {
		t.Fatalf("jsonl import code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var imported map[string]any
	if err := json.Unmarshal(out.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if got := int(imported["rows"].(float64)); got < 9 {
		t.Fatalf("jsonl imported rows = %d, payload=%#v", got, imported)
	}
	code, out, errOut = runForTest("--json", "--db", jsonlImportDB, "search", "boarding")
	if code != 0 {
		t.Fatalf("jsonl search code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) < 2 {
		t.Fatalf("jsonl search hits = %#v", hits)
	}
	syncImportDB := filepath.Join(root, "sync-imported.db")
	code, out, errOut = runForTest("--json", "--db", syncImportDB, "sync", "--source", "import", "--import-path", jsonlPath)
	if code != 0 {
		t.Fatalf("sync import code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported["source"] != "import-jsonl" {
		t.Fatalf("sync import payload=%#v", imported)
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
	var favorites int
	if err := db.QueryRowContext(context.Background(), `select count(*) from favorites`).Scan(&favorites); err != nil {
		t.Fatal(err)
	}
	if favorites != 1 {
		t.Fatalf("imported favorites = %d", favorites)
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

func TestCLISyncDesktopBackup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	backupRoot := filepath.Join(root, "backup-root")
	createFixtureDB(t, filepath.Join(backupRoot, "db_storage", "message", "message_0.db"))

	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "sync", "--source", "desktop-backup", "--backup-root", backupRoot)
	if code != 0 {
		t.Fatalf("backup sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["source"] != "desktop-backup" {
		t.Fatalf("payload = %#v", payload)
	}
	if got := int(payload["imported_messages"].(float64)); got != 2 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_favorites"].(float64)); got != 1 {
		t.Fatalf("imported_favorites = %d, payload=%#v", got, payload)
	}
	code, out, errOut = runForTest("--json", "runs", "--limit", "1")
	if code != 0 {
		t.Fatalf("runs code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var runs map[string]any
	if err := json.Unmarshal(out.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	values := runs["values"].([]any)
	if len(values) != 1 || values[0].(map[string]any)["source"] != "desktop-backup" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestCLISyncAllAggregatesConfiguredAndExplicitSources(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	cacheRoot := filepath.Join(root, "cache")
	cfgPath := filepath.Join(configRoot, "weicrawl", "config.toml")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("WEICRAWL_CACHE_DIR", cacheRoot)
	t.Setenv("WEICRAWL_CONFIG", cfgPath)
	t.Setenv("WEICRAWL_WECHAT_APP_ID", "configured-but-disabled")
	t.Setenv("WEICRAWL_WECHAT_APP_SECRET", "configured-but-disabled")

	container := filepath.Join(root, "WeChatContainer")
	profileRoot := filepath.Join(container, "Data", "Documents", "xwechat_files", "wxid_all_abcd")
	createFixtureDB(t, filepath.Join(profileRoot, "db_storage", "message", "message_0.db"))
	backupRoot := filepath.Join(root, "backup-root")
	createFixtureDB(t, filepath.Join(backupRoot, "db_storage", "message", "message_0.db"))

	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	patchConfigPath(t, cfgPath, container)
	code, out, errOut = runForTest("--json", "sync", "--source", "all", "--profile", "wxid_all", "--backup-root", backupRoot)
	if code != 0 {
		t.Fatalf("sync all code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["source"] != "all" || payload["status"] != "success" {
		t.Fatalf("payload = %#v", payload)
	}
	sources := map[string]bool{}
	for _, value := range payload["results"].([]any) {
		item := value.(map[string]any)
		sources[fmt.Sprint(item["source"])] = true
	}
	if !sources["desktop-macos"] || !sources["desktop-backup"] {
		t.Fatalf("sources = %#v payload=%#v", sources, payload)
	}
	if sources["official-account-api"] {
		t.Fatalf("official account should not run unless enabled: %#v", payload)
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
	if got := int(payload["imported_raw_records"].(float64)); got != 1 {
		t.Fatalf("imported_raw_records = %d, payload=%#v", got, payload)
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

func TestCLIUnlockDecryptThenSyncEncryptedFixture(t *testing.T) {
	sqlcipher, err := exec.LookPath("sqlcipher")
	if err != nil {
		sqlcipher = "/opt/homebrew/opt/sqlcipher/bin/sqlcipher"
		if _, statErr := os.Stat(sqlcipher); statErr != nil {
			t.Skip("sqlcipher unavailable")
		}
	}
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plain := filepath.Join(root, "plain.db")
	createNativeMessageDB(t, plain, "alice")
	snapshotRoot := filepath.Join(root, "snapshot")
	encrypted := filepath.Join(snapshotRoot, "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(encrypted), 0o755); err != nil {
		t.Fatal(err)
	}
	encryptSQLiteFixture(t, sqlcipher, plain, encrypted, key)
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"message/message_0.db":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	decryptedDir := filepath.Join(root, "decrypted")
	code, out, errOut := runForTest("--json", "unlock", "desktop", "--keys", keysPath, "--snapshot", snapshotRoot, "--out", decryptedDir)
	if code != 0 {
		t.Fatalf("unlock code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "sync", "--source", "desktop-macos", "--profile", "profile-decrypted", "--decrypted-dir", decryptedDir)
	if code != 0 {
		t.Fatalf("sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := int(payload["imported_messages"].(float64)); got != 1 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
}

func TestCLIKeyScanRequiresExplicitProcessInspect(t *testing.T) {
	code, _, errOut := runForTest("--json", "unlock", "scan-keys")
	if code == 0 {
		t.Fatal("scan-keys succeeded without --allow-process-inspect")
	}
	if !strings.Contains(errOut.String(), "--allow-process-inspect") {
		t.Fatalf("stderr = %s", errOut.String())
	}
	code, out, errOut := runForTest("--json", "unlock", "scan-keys", "--allow-process-inspect", "--script", "/tmp/find_key_memscan.py")
	if code != 0 {
		t.Fatalf("scan plan code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var plan map[string]any
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan["execute"].(bool) {
		t.Fatalf("plan unexpectedly executes: %#v", plan)
	}
}

func runForTest(args ...string) (int, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	return code, &stdout, &stderr
}

func readJSONLEntityCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	counts := map[string]int{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		counts[fmt.Sprint(row["entity"])]++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return counts
}

func doctorCheck(t *testing.T, doctor map[string]any, id string) map[string]any {
	t.Helper()
	checks := doctor["checks"].([]any)
	for _, value := range checks {
		check := value.(map[string]any)
		if check["id"] == id {
			return check
		}
	}
	t.Fatalf("doctor check %q missing: %#v", id, checks)
	return nil
}

func readTUIKindCounts(t *testing.T, data []byte) map[string]int {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range rows {
		kind := fmt.Sprint(row["kind"])
		switch {
		case strings.HasPrefix(kind, "message:"):
			counts["message:"]++
		case strings.HasPrefix(kind, "favorite:"):
			counts["favorite:"]++
		case strings.HasPrefix(kind, "media:"):
			counts["media:"]++
		default:
			counts[kind]++
		}
	}
	return counts
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
insert into "` + table + `" values(7, 1, 1781323200, 'alice', 'native hello from decrypted shape', '0');
create table NativeExtra(id text, body text);
insert into NativeExtra values('extra-1', 'preserve me');`)
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

func encryptSQLiteFixture(t *testing.T, sqlcipher, src, dst, key string) {
	t.Helper()
	cmd := exec.Command(sqlcipher, src)
	cmd.Stdin = strings.NewReader(`ATTACH DATABASE '` + dst + `' AS encrypted KEY "x'` + key + `'";
SELECT sqlcipher_export('encrypted');
DETACH DATABASE encrypted;
`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("encrypt fixture: %v\n%s", err, out)
	}
}

func createFixtureDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := `
create table weicrawl_fixture_contacts(contact_id text, alias text, display_name text, remark_name text, kind text, avatar_ref text, raw_json text);
create table weicrawl_fixture_chats(chat_id text, kind text, title text, last_message_at text, unread_count integer, muted integer, pinned integer, raw_json text);
create table weicrawl_fixture_messages(message_id text, chat_id text, sender_id text, direction text, message_type text, sent_at text, text text, normalized_text text, source_rowid text, raw_json text);
create table weicrawl_fixture_message_parts(message_id text, part_index integer, kind text, text text, media_id text, url text, raw_json text);
create table weicrawl_fixture_message_events(chat_id text, message_id text, event_type text, event_at text, payload_json text);
create table weicrawl_fixture_favorites(favorite_id text, kind text, title text, text text, source_ref text, raw_json text);
create table weicrawl_fixture_moments(moment_id text, author_id text, text text, created_at text, raw_json text);
insert into weicrawl_fixture_contacts values('alice', 'alice', 'Alice', '', 'user', '', '{}');
insert into weicrawl_fixture_chats values('chat-1', 'direct', 'Alice', '2026-06-13T01:00:00Z', 0, 0, 0, '{}');
insert into weicrawl_fixture_messages values('m1', 'chat-1', 'alice', 'inbound', 'text', '2026-06-13T01:00:00Z', 'hello from fixture', 'hello from fixture', '1', '{}');
insert into weicrawl_fixture_messages values('m2', 'chat-1', 'alice', 'inbound', 'text', '2026-06-13T02:00:00Z', '航班 changed', '航班 changed', '2', '{}');
insert into weicrawl_fixture_message_parts values('m2', 0, 'link', 'boarding pass link', '', 'https://example.invalid/boarding', '{}');
insert into weicrawl_fixture_message_events values('chat-1', 'm2', 'edited', '2026-06-13T02:05:00Z', '{"reason":"fixture"}');
insert into weicrawl_fixture_favorites values('fav-1', 'message', 'Boarding pass', 'saved boarding pass note', 'm2', '{}');
insert into weicrawl_fixture_moments values('moment-1', 'alice', 'timeline launch note', '2026-06-13T03:00:00Z', '{}');
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
