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
	code, out, errOut = runForTest("--json", "media")
	if code != 0 {
		t.Fatalf("media list code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var mediaList map[string]any
	if err := json.Unmarshal(out.Bytes(), &mediaList); err != nil {
		t.Fatal(err)
	}
	mediaValues := mediaList["values"].([]any)
	if len(mediaValues) != 1 {
		t.Fatalf("media values = %#v", mediaValues)
	}
	metadataOnlyMedia := mediaValues[0].(map[string]any)
	if metadataOnlyMedia["archive_path"] != nil && fmt.Sprint(metadataOnlyMedia["archive_path"]) != "" {
		t.Fatalf("metadata-only media should not expose archive_path: %#v", metadataOnlyMedia)
	}
	if !strings.Contains(fmt.Sprint(metadataOnlyMedia["source_path"]), "sample.txt") {
		t.Fatalf("metadata-only media source_path missing relative filename: %#v", metadataOnlyMedia)
	}
	code, out, errOut = runForTest("--json", "sync", "--profile", "wxid_fixture", "--include-media", "--media-mode", "copy", "--keep-source-snapshot", "--concurrency", "2")
	if code != 0 {
		t.Fatalf("sync media copy code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &sync); err != nil {
		t.Fatal(err)
	}
	if got := int(sync["concurrency"].(float64)); got != 2 {
		t.Fatalf("sync concurrency = %d, payload=%#v", got, sync)
	}
	mediaSnapshot := fmt.Sprint(sync["snapshot_path"])
	if mediaSnapshot == "" {
		t.Fatalf("media copy snapshot missing: %#v", sync)
	}
	copiedMedia := filepath.Join(mediaSnapshot, "media", "msg", "file", "2026-06", "sample.txt")
	if bytes, err := os.ReadFile(copiedMedia); err != nil || string(bytes) != "sample media" {
		t.Fatalf("copied media = %q err=%v", string(bytes), err)
	}
	code, out, errOut = runForTest("--json", "media")
	if code != 0 {
		t.Fatalf("media list after copy code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &mediaList); err != nil {
		t.Fatal(err)
	}
	copiedMediaRow := mediaList["values"].([]any)[0].(map[string]any)
	if fmt.Sprint(copiedMediaRow["archive_path"]) != copiedMedia {
		t.Fatalf("copied media archive_path = %#v want %s", copiedMediaRow, copiedMedia)
	}
	code, out, errOut = runForTest("--json", "sync", "--profile", "wxid_fixture", "--include-media")
	if code != 0 {
		t.Fatalf("sync media metadata after copy code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "media")
	if code != 0 {
		t.Fatalf("media list after metadata resync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &mediaList); err != nil {
		t.Fatal(err)
	}
	resyncedMediaRow := mediaList["values"].([]any)[0].(map[string]any)
	if fmt.Sprint(resyncedMediaRow["archive_path"]) != copiedMedia {
		t.Fatalf("metadata resync should preserve copied archive_path: %#v want %s", resyncedMediaRow, copiedMedia)
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
	lastRun := archiveStatus["last_sync_run"].(map[string]any)
	if got := int(lastRun["imported_message_parts"].(float64)); got != 1 {
		t.Fatalf("last run imported_message_parts = %d, run=%#v", got, lastRun)
	}
	if got := int(lastRun["imported_message_events"].(float64)); got != 1 {
		t.Fatalf("last run imported_message_events = %d, run=%#v", got, lastRun)
	}
	if got := int(lastRun["imported_favorites"].(float64)); got != 1 {
		t.Fatalf("last run imported_favorites = %d, run=%#v", got, lastRun)
	}
	if got := int(lastRun["imported_moments"].(float64)); got != 1 {
		t.Fatalf("last run imported_moments = %d, run=%#v", got, lastRun)
	}
	sourceStatus := status["source"].(map[string]any)["desktop_macos"].(map[string]any)
	if got := int(sourceStatus["profile_count"].(float64)); got != 1 {
		t.Fatalf("source profile_count = %d, source=%#v", got, sourceStatus)
	}
	if got := int(sourceStatus["database_count"].(float64)); got != 1 {
		t.Fatalf("source database_count = %d, source=%#v", got, sourceStatus)
	}
	if _, ok := sourceStatus["app_version"]; !ok {
		t.Fatalf("source app_version missing: %#v", sourceStatus)
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
	code, out, errOut = runForTest("--json", "search", "Alice")
	if code != 0 {
		t.Fatalf("search Alice code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	entities := searchEntitySet(search["hits"].([]any))
	if !entities["contact"] || !entities["chat"] {
		t.Fatalf("Alice search entities = %#v hits=%#v", entities, search["hits"])
	}
	code, out, errOut = runForTest("--json", "search", "sample.txt")
	if code != 0 {
		t.Fatalf("search media code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	entities = searchEntitySet(search["hits"].([]any))
	if !entities["media"] {
		t.Fatalf("media search entities = %#v hits=%#v", entities, search["hits"])
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
		"chat_member":   1,
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
	for command, want := range map[string]int{
		"chat-members":   1,
		"message-parts":  1,
		"message-events": 1,
		"moments":        1,
	} {
		code, out, errOut = runForTest("--json", command)
		if code != 0 {
			t.Fatalf("%s code=%d stderr=%s stdout=%s", command, code, errOut, out)
		}
		var listed map[string]any
		if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
		if got := len(listed["values"].([]any)); got != want {
			t.Fatalf("%s values = %d, payload=%#v", command, got, listed)
		}
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
	syncSinceDB := filepath.Join(root, "sync-imported-since.db")
	code, out, errOut = runForTest("--json", "--db", syncSinceDB, "init")
	if code != 0 {
		t.Fatalf("sync since init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "--db", syncSinceDB, "sync", "--source", "import", "--import-path", jsonlPath, "--since", "2026-06-13T01:30:00Z")
	if code != 0 {
		t.Fatalf("sync import since code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	importCounts := imported["counts"].(map[string]any)
	if got := int(importCounts["message"].(float64)); got != 1 {
		t.Fatalf("sync import since message count = %d, payload=%#v", got, imported)
	}
	if got := int(importCounts["message_part"].(float64)); got != 1 {
		t.Fatalf("sync import since message_part count = %d, payload=%#v", got, imported)
	}
	if got := int(importCounts["message_event"].(float64)); got != 1 {
		t.Fatalf("sync import since message_event count = %d, payload=%#v", got, imported)
	}
	if got := int(importCounts["favorite"].(float64)); got != 1 {
		t.Fatalf("sync import since favorite count = %d, payload=%#v", got, imported)
	}
	if got := int(importCounts["moment"].(float64)); got != 1 {
		t.Fatalf("sync import since moment count = %d, payload=%#v", got, imported)
	}
	code, out, errOut = runForTest("--json", "--db", syncSinceDB, "search", "hello from fixture")
	if code != 0 {
		t.Fatalf("sync import since old search code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) != 0 {
		t.Fatalf("sync import since old hits = %#v", hits)
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

func TestUnlockDesktopExplainWithKeysIsDryRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotRoot := filepath.Join(root, "snapshot")
	snapshotDB := filepath.Join(snapshotRoot, "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotDB, []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"message/message_0.db":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sqlcipher := filepath.Join(root, "sqlcipher")
	if err := os.WriteFile(sqlcipher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "decrypted")
	code, out, errOut := runForTest("--json", "unlock", "desktop", "--explain", "--keys", keysPath, "--snapshot", snapshotRoot, "--sqlcipher", sqlcipher)
	if code != 0 {
		t.Fatalf("unlock explain code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload["dry_run"].(bool) || !payload["available"].(bool) {
		t.Fatalf("payload = %#v", payload)
	}
	check := payload["check"].(map[string]any)
	if int(check["key_count"].(float64)) != 1 {
		t.Fatalf("check = %#v", check)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created output dir: %v", err)
	}
}

func TestDoctorProbeUnlockChecksManifestSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotRoot := filepath.Join(root, "snapshot")
	snapshotDB := filepath.Join(snapshotRoot, "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotDB, []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"message/message_0.db":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sqlcipher := filepath.Join(root, "sqlcipher")
	if err := os.WriteFile(sqlcipher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "doctor", "--probe-unlock", "--keys", keysPath, "--snapshot", snapshotRoot, "--sqlcipher", sqlcipher)
	if code != 0 {
		t.Fatalf("doctor code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(t, payload, "unlock_readiness")
	if !check["ok"].(bool) {
		t.Fatalf("unlock readiness check = %#v", check)
	}
	readiness := check["check"].(map[string]any)
	if int(readiness["key_count"].(float64)) != 1 {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestDoctorProbeUnlockReportsMissingInputs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	code, out, errOut := runForTest("--json", "doctor", "--probe-unlock")
	if code != 0 {
		t.Fatalf("doctor code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(t, payload, "unlock_readiness")
	if check["ok"].(bool) || !check["skipped"].(bool) {
		t.Fatalf("unlock readiness check = %#v", check)
	}
	missing := fmt.Sprint(check["missing"])
	if !strings.Contains(missing, "--keys") || !strings.Contains(missing, "--snapshot") {
		t.Fatalf("missing inputs = %#v", check["missing"])
	}
}

func TestUnlockForgetReportsNoPersistedMaterial(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	code, out, errOut := runForTest("--json", "unlock", "forget", "--profile", "alice")
	if code != 0 {
		t.Fatalf("unlock forget code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["forgotten"].(bool) {
		t.Fatalf("forget claimed persisted deletion: %#v", payload)
	}
	if available := payload["available"].(bool); available {
		t.Fatalf("forget unexpectedly available: %#v", payload)
	}
	if !strings.Contains(fmt.Sprint(payload["warning"]), "no persisted unlock material") {
		t.Fatalf("forget warning missing: %#v", payload)
	}
}

func TestHelpListsAllGlobalFlags(t *testing.T) {
	code, out, errOut := runForTest("help")
	if code != 0 {
		t.Fatalf("help code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	for _, flag := range []string{"--config", "--db", "--profile", "--json", "--quiet", "--verbose"} {
		if !strings.Contains(out.String(), flag) {
			t.Fatalf("help missing %s:\n%s", flag, out.String())
		}
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

func TestCLISQLIsReadOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	dbPath := filepath.Join(root, "archive.db")
	code, out, errOut := runForTest("--json", "--db", dbPath, "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "--db", dbPath, "sql", "select count(*) as n from messages")
	if code != 0 {
		t.Fatalf("sql select code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload["values"].([]any)) != 1 {
		t.Fatalf("sql values = %#v", payload)
	}
	code, _, errOut = runForTest("--json", "--db", dbPath, "sql", "delete from messages")
	if code == 0 {
		t.Fatal("sql delete succeeded without explicit write support")
	}
	if !strings.Contains(errOut.String(), "read-only") {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestMetadataAdvertisesArchiveSurfaces(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	code, out, errOut := runForTest("--json", "metadata")
	if code != 0 {
		t.Fatalf("metadata code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	commands := payload["commands"].(map[string]any)
	for _, name := range []string{"version", "init", "doctor", "metadata", "status", "sync", "unlock", "profiles", "contacts", "chats", "chat-members", "messages", "message-parts", "message-events", "favorites", "biz-accounts", "articles", "media", "moments", "raw-records", "runs", "sql", "export", "snapshot", "import", "tui", "completion"} {
		if _, ok := commands[name]; !ok {
			t.Fatalf("metadata command %q missing: %#v", name, commands)
		}
	}
	syncCommand := commands["sync"].(map[string]any)
	argv := syncCommand["argv"].([]any)
	if got := fmt.Sprint(argv[len(argv)-2]) + " " + fmt.Sprint(argv[len(argv)-1]); got != "--source all" {
		t.Fatalf("sync argv = %#v", argv)
	}
	for _, name := range []string{"init", "sync", "snapshot", "import"} {
		if !commands[name].(map[string]any)["mutates"].(bool) {
			t.Fatalf("command %q should be marked mutating: %#v", name, commands[name])
		}
	}
	if commands["completion"].(map[string]any)["json"] != nil {
		t.Fatalf("completion should not advertise JSON output: %#v", commands["completion"])
	}
	capabilities := map[string]bool{}
	for _, value := range payload["capabilities"].([]any) {
		capabilities[fmt.Sprint(value)] = true
	}
	for _, name := range []string{"desktop-backup", "jsonl-import", "official-account-api", "unlock-sync"} {
		if !capabilities[name] {
			t.Fatalf("capability %q missing: %#v", name, capabilities)
		}
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
	run := values[0].(map[string]any)
	if got := int(run["imported_message_parts"].(float64)); got != 1 {
		t.Fatalf("run imported_message_parts = %d, run=%#v", got, run)
	}
	if got := int(run["imported_favorites"].(float64)); got != 1 {
		t.Fatalf("run imported_favorites = %d, run=%#v", got, run)
	}

	sinceDB := filepath.Join(root, "since.db")
	code, out, errOut = runForTest("--json", "--db", sinceDB, "init")
	if code != 0 {
		t.Fatalf("since init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	code, out, errOut = runForTest("--json", "--db", sinceDB, "sync", "--source", "desktop-backup", "--backup-root", backupRoot, "--since", "2026-06-13T01:30:00Z")
	if code != 0 {
		t.Fatalf("backup since sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["since"] != "2026-06-13T01:30:00Z" {
		t.Fatalf("since payload = %#v", payload)
	}
	if got := int(payload["imported_messages"].(float64)); got != 1 {
		t.Fatalf("since imported_messages = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_message_parts"].(float64)); got != 1 {
		t.Fatalf("since imported_message_parts = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_message_events"].(float64)); got != 1 {
		t.Fatalf("since imported_message_events = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_favorites"].(float64)); got != 1 {
		t.Fatalf("since imported_favorites = %d, payload=%#v", got, payload)
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

func TestCLISyncDesktopMarksEncryptedLikeDBPartial(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	cfgPath := filepath.Join(configRoot, "weicrawl", "config.toml")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("WEICRAWL_CONFIG", cfgPath)

	container := filepath.Join(root, "WeChatContainer")
	dbPath := filepath.Join(container, "Data", "Documents", "xwechat_files", "wxid_locked_abcd", "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("encrypted-ish"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	patchConfigPath(t, cfgPath, container)
	code, out, errOut = runForTest("--json", "sync", "--profile", "wxid_locked")
	if code != 0 {
		t.Fatalf("sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "partial" {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(fmt.Sprint(payload["warnings"]), "encrypted or unsupported") {
		t.Fatalf("warnings = %#v", payload["warnings"])
	}
}

func TestCLISyncAllPropagatesPartialSourceStatus(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	cfgPath := filepath.Join(configRoot, "weicrawl", "config.toml")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("WEICRAWL_CONFIG", cfgPath)

	container := filepath.Join(root, "WeChatContainer")
	dbPath := filepath.Join(container, "Data", "Documents", "xwechat_files", "wxid_partial_abcd", "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("encrypted-ish"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	patchConfigPath(t, cfgPath, container)
	code, out, errOut = runForTest("--json", "sync", "--source", "all", "--profile", "wxid_partial")
	if code != 0 {
		t.Fatalf("sync all code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "partial" {
		t.Fatalf("payload = %#v", payload)
	}
	results := payload["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["status"] != "partial" {
		t.Fatalf("results = %#v", results)
	}
}

func TestCLISyncAllReportsSkippedWhenConfiguredSourcesSkip(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config", "weicrawl", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("WEICRAWL_CONFIG", cfgPath)
	t.Setenv("APP_ID_ENV", "")
	t.Setenv("APP_SECRET_ENV", "")
	configBody := fmt.Sprintf(`
[archive]
db_path = %q
cache_dir = %q
log_dir = %q

[desktop_macos]
enabled = false
container_path = %q

[official_account]
enabled = true
app_id_env = "APP_ID_ENV"
app_secret_env = "APP_SECRET_ENV"
`, filepath.Join(root, "weicrawl.db"), filepath.Join(root, "cache"), filepath.Join(root, "logs"), filepath.Join(root, "empty-container"))
	if err := os.WriteFile(cfgPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "sync", "--source", "all")
	if code != 0 {
		t.Fatalf("sync all code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "skipped" {
		t.Fatalf("payload = %#v", payload)
	}
	results := payload["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["status"] != "skipped" {
		t.Fatalf("results = %#v", results)
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
	createNativeBizMessageDB(t, filepath.Join(profileRoot, "db_storage", "message", "message_biz.db"), "gh_news")

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
	if got := int(payload["imported_messages"].(float64)); got != 4 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_message_parts"].(float64)); got != 3 {
		t.Fatalf("imported_message_parts = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_media"].(float64)); got != 1 {
		t.Fatalf("imported_media = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_biz_accounts"].(float64)); got != 1 {
		t.Fatalf("imported_biz_accounts = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_articles"].(float64)); got != 2 {
		t.Fatalf("imported_articles = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_favorites"].(float64)); got != 1 {
		t.Fatalf("imported_favorites = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_moments"].(float64)); got != 1 {
		t.Fatalf("imported_moments = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_raw_records"].(float64)); got != 1 {
		t.Fatalf("imported_raw_records = %d, payload=%#v", got, payload)
	}
	code, out, errOut = runForTest("--json", "articles")
	if code != 0 {
		t.Fatalf("articles code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var articles map[string]any
	if err := json.Unmarshal(out.Bytes(), &articles); err != nil {
		t.Fatal(err)
	}
	if got := len(articles["values"].([]any)); got != 2 {
		t.Fatalf("articles values = %d, payload=%#v", got, articles)
	}
	code, out, errOut = runForTest("--json", "sql", "select count(*) as n from biz_accounts")
	if code != 0 {
		t.Fatalf("biz sql code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var bizAccounts map[string]any
	if err := json.Unmarshal(out.Bytes(), &bizAccounts); err != nil {
		t.Fatal(err)
	}
	if got := int(bizAccounts["values"].([]any)[0].(map[string]any)["n"].(float64)); got != 1 {
		t.Fatalf("biz account count = %d, payload=%#v", got, bizAccounts)
	}
	code, out, errOut = runForTest("--json", "biz-accounts")
	if code != 0 {
		t.Fatalf("biz-accounts code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &bizAccounts); err != nil {
		t.Fatal(err)
	}
	if got := len(bizAccounts["values"].([]any)); got != 1 {
		t.Fatalf("biz-accounts values = %d, payload=%#v", got, bizAccounts)
	}
	code, out, errOut = runForTest("--json", "media")
	if code != 0 {
		t.Fatalf("media code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var media map[string]any
	if err := json.Unmarshal(out.Bytes(), &media); err != nil {
		t.Fatal(err)
	}
	if got := len(media["values"].([]any)); got != 1 {
		t.Fatalf("media values = %d, payload=%#v", got, media)
	}
	code, out, errOut = runForTest("--json", "favorites")
	if code != 0 {
		t.Fatalf("favorites code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var favorites map[string]any
	if err := json.Unmarshal(out.Bytes(), &favorites); err != nil {
		t.Fatal(err)
	}
	if got := len(favorites["values"].([]any)); got != 1 {
		t.Fatalf("favorites values = %d, payload=%#v", got, favorites)
	}
	code, out, errOut = runForTest("--json", "moments")
	if code != 0 {
		t.Fatalf("moments code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var moments map[string]any
	if err := json.Unmarshal(out.Bytes(), &moments); err != nil {
		t.Fatal(err)
	}
	if got := len(moments["values"].([]any)); got != 1 {
		t.Fatalf("moments values = %d, payload=%#v", got, moments)
	}
	code, out, errOut = runForTest("--json", "raw-records")
	if code != 0 {
		t.Fatalf("raw-records code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var rawRecords map[string]any
	if err := json.Unmarshal(out.Bytes(), &rawRecords); err != nil {
		t.Fatal(err)
	}
	if got := len(rawRecords["values"].([]any)); got != 1 {
		t.Fatalf("raw-records values = %d, payload=%#v", got, rawRecords)
	}
	code, out, errOut = runForTest("--json", "search", "decrypted shape")
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
	if got := int(payload["imported_messages"].(float64)); got != 3 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_articles"].(float64)); got != 1 {
		t.Fatalf("imported_articles = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_media"].(float64)); got != 1 {
		t.Fatalf("imported_media = %d, payload=%#v", got, payload)
	}
	code, out, errOut = runForTest("--json", "sync", "--source", "desktop-macos", "--profile", "profile-decrypted", "--decrypted-dir", decrypted, "--keep-decrypted-snapshot")
	if code != 0 {
		t.Fatalf("sync keep decrypted code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(payload["decrypted_snapshot_path"]) != decrypted {
		t.Fatalf("decrypted snapshot path missing: %#v", payload)
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
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	decryptedDir := filepath.Join(root, "decrypted")
	code, out, errOut := runForTest("--json", "unlock", "desktop", "--keys", keysPath, "--snapshot", snapshotRoot, "--out", decryptedDir)
	if code != 0 {
		t.Fatalf("unlock code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var unlockPayload map[string]any
	if err := json.Unmarshal(out.Bytes(), &unlockPayload); err != nil {
		t.Fatal(err)
	}
	if unlockPayload["method"] != "key-manifest+sqlcipher" || unlockPayload["persisted"].(bool) {
		t.Fatalf("unlock payload = %#v", unlockPayload)
	}
	decrypt := unlockPayload["decrypt"].(map[string]any)
	if len(decrypt["decrypted"].([]any)) != 1 {
		t.Fatalf("decrypt payload = %#v", decrypt)
	}
	if !strings.Contains(fmt.Sprint(unlockPayload["next"]), "--decrypted-dir") {
		t.Fatalf("unlock next missing decrypted dir: %#v", unlockPayload)
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
	if got := int(payload["imported_messages"].(float64)); got != 3 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_articles"].(float64)); got != 1 {
		t.Fatalf("imported_articles = %d, payload=%#v", got, payload)
	}
	if got := int(payload["imported_media"].(float64)); got != 1 {
		t.Fatalf("imported_media = %d, payload=%#v", got, payload)
	}
}

func TestCLIUnlockDesktopSyncImportsEncryptedFixture(t *testing.T) {
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
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	decryptedDir := filepath.Join(root, "decrypted")
	code, out, errOut = runForTest("--json", "unlock", "desktop", "--sync", "--profile", "profile-decrypted", "--keys", keysPath, "--snapshot", snapshotRoot, "--out", decryptedDir)
	if code != 0 {
		t.Fatalf("unlock sync code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["method"] != "key-manifest+sqlcipher" || payload["sync"] == nil {
		t.Fatalf("unlock sync payload = %#v", payload)
	}
	syncPayload := payload["sync"].(map[string]any)
	if got := int(syncPayload["imported_messages"].(float64)); got != 3 {
		t.Fatalf("imported_messages = %d, payload=%#v", got, syncPayload)
	}
	if !payload["decrypted_removed"].(bool) {
		t.Fatalf("decrypted output was not removed: %#v", payload)
	}
	if _, err := os.Stat(decryptedDir); !os.IsNotExist(err) {
		t.Fatalf("decrypted output still exists: %v", err)
	}
	code, out, errOut = runForTest("--json", "search", "decrypted shape")
	if code != 0 {
		t.Fatalf("search code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var search map[string]any
	if err := json.Unmarshal(out.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if hits := search["hits"].([]any); len(hits) == 0 {
		t.Fatalf("search hits = %#v", hits)
	}
}

func TestCLIUnlockDesktopSyncCanKeepDecryptedOutput(t *testing.T) {
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
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "init")
	if code != 0 {
		t.Fatalf("init code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	decryptedDir := filepath.Join(root, "decrypted")
	code, out, errOut = runForTest("--json", "unlock", "desktop", "--sync", "--keep-decrypted-snapshot", "--profile", "profile-decrypted", "--keys", keysPath, "--snapshot", snapshotRoot, "--out", decryptedDir)
	if code != 0 {
		t.Fatalf("unlock sync keep code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["decrypted_removed"] != nil {
		t.Fatalf("decrypted output should not be removed: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(decryptedDir, "message", "message_0.db")); err != nil {
		t.Fatalf("decrypted output missing: %v", err)
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

func TestCLIKeyScanExecuteRedactsKeyMaterial(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scanner")
	manifestPath := filepath.Join(root, "wechat_keys.json")
	key := strings.Repeat("0", 64)
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'db key: %064d\n' 0
printf "wrapped key: x'%064d'\n" 1
`), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "unlock", "scan-keys", "--allow-process-inspect", "--execute", "--script", script, "--scan-out", manifestPath)
	if code != 0 {
		t.Fatalf("scan execute code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if strings.Contains(out.String(), key) {
		t.Fatalf("scan output leaked key material: %s", out.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload["redacted"].(bool) {
		t.Fatalf("payload did not mark redaction: %#v", payload)
	}
	if !payload["manifest_written"].(bool) || fmt.Sprint(payload["manifest_path"]) != manifestPath {
		t.Fatalf("manifest fields missing: %#v", payload)
	}
	if !strings.Contains(fmt.Sprint(payload["output_redacted"]), "[redacted-key]") {
		t.Fatalf("payload = %#v", payload)
	}
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), `"__default_key"`) || !strings.Contains(string(bytes), key) {
		t.Fatalf("manifest = %s", bytes)
	}
}

func TestCLIKeyScanExecuteKeepsExtractorManifest(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scanner")
	manifestPath := filepath.Join(root, "wechat_keys.json")
	perDBKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	stdoutKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat > "$WEICRAWL_SCAN_OUT" <<'JSON'
{"keys":{"message/message_0.db":"`+perDBKey+`"}}
JSON
printf 'db key: `+stdoutKey+`\n'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "unlock", "scan-keys", "--allow-process-inspect", "--execute", "--script", script, "--scan-out", manifestPath)
	if code != 0 {
		t.Fatalf("scan execute code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["manifest_written"].(bool) {
		t.Fatalf("expected extractor manifest reuse: %#v", payload)
	}
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), stdoutKey) || !strings.Contains(string(bytes), perDBKey) {
		t.Fatalf("manifest = %s", bytes)
	}
}

func TestCLIKeyScanExecuteWritesStdoutManifest(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scanner")
	manifestPath := filepath.Join(root, "wechat_keys.json")
	perDBKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '[*] scanning\n'
printf '{"keys":{"message/message_0.db":"`+perDBKey+`"}}'
printf '\n[*] done\n'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runForTest("--json", "unlock", "scan-keys", "--allow-process-inspect", "--execute", "--script", script, "--scan-out", manifestPath)
	if code != 0 {
		t.Fatalf("scan execute code=%d stderr=%s stdout=%s", code, errOut, out)
	}
	if strings.Contains(out.String(), perDBKey) {
		t.Fatalf("scan output leaked key material: %s", out.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload["manifest_written"].(bool) {
		t.Fatalf("expected stdout manifest write: %#v", payload)
	}
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), perDBKey) || strings.Contains(string(bytes), "__default_key") {
		t.Fatalf("manifest = %s", bytes)
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

func searchEntitySet(hits []any) map[string]bool {
	entities := map[string]bool{}
	for _, value := range hits {
		hit := value.(map[string]any)
		entities[fmt.Sprint(hit["entity"])] = true
	}
	return entities
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
create table FavoriteTable(local_id text, type text, title text, content text, source_ref text, create_time integer);
create table SnsTimeline(sns_id text, user_name text, content text, create_time integer);
create table NativeExtra(id text, body text);
insert into NativeExtra values('extra-1', 'preserve me');`)
	if err != nil {
		t.Fatal(err)
	}
	linkXML := `<msg><appmsg><title>Native launch post</title><des>Rich link summary</des><url>https://example.invalid/native</url><appname>WeChat</appname></appmsg></msg>`
	imageXML := `<msg><img cdnthumburl="https://example.invalid/native-thumb.jpg"></img></msg>`
	_, err = db.Exec(`insert into "`+table+`" values(7, 1, 1781323200, 'alice', 'native hello from decrypted shape', '0');
insert into "`+table+`" values(8, 49, 1781323300, 'alice', ?, '0');
insert into "`+table+`" values(9, 3, 1781323400, 'alice', ?, '0');
insert into FavoriteTable values('fav-native-1', 'message', 'Native favorite', 'native favorite text', 'message:8', 1781323500);
insert into SnsTimeline values('sns-native-1', 'alice', 'native moment text', 1781323600);`, linkXML, imageXML)
	if err != nil {
		t.Fatal(err)
	}
}

func createNativeBizMessageDB(t *testing.T, path, username string) {
	t.Helper()
	db := openFixtureDB(t, path)
	defer db.Close()
	table := nativeMsgTable(username)
	_, err := db.Exec(`create table Name2Id(user_name text);
insert into Name2Id values(?);`, username)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table "` + table + `"(local_id integer, local_type integer, create_time integer, real_sender_id text, message_content text, source text);`)
	if err != nil {
		t.Fatal(err)
	}
	linkXML := `<msg><appmsg><title>Public account post</title><des>Biz summary</des><url>https://example.invalid/biz</url><appname>WeChat</appname></appmsg></msg>`
	_, err = db.Exec(`insert into "`+table+`" values(11, 49, 1781323700, 'gh_news', ?, '0');`, linkXML)
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
create table weicrawl_fixture_chat_members(chat_id text, contact_id text, display_name text, raw_json text);
create table weicrawl_fixture_messages(message_id text, chat_id text, sender_id text, direction text, message_type text, sent_at text, text text, normalized_text text, source_rowid text, raw_json text);
create table weicrawl_fixture_message_parts(message_id text, part_index integer, kind text, text text, media_id text, url text, raw_json text);
create table weicrawl_fixture_message_events(chat_id text, message_id text, event_type text, event_at text, payload_json text);
create table weicrawl_fixture_favorites(favorite_id text, kind text, title text, text text, source_ref text, raw_json text);
create table weicrawl_fixture_moments(moment_id text, author_id text, text text, created_at text, raw_json text);
insert into weicrawl_fixture_contacts values('alice', 'alice', 'Alice', '', 'user', '', '{}');
insert into weicrawl_fixture_chats values('chat-1', 'direct', 'Alice', '2026-06-13T01:00:00Z', 0, 0, 0, '{}');
insert into weicrawl_fixture_chat_members values('chat-1', 'alice', 'Alice', '{}');
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
