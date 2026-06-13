package unlock

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadKeyManifestAcceptsWechatKeyShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`{
  "message/message_0.db": "`+key+`",
  "__salts__": ["ignored"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Keys["message/message_0.db"] != key {
		t.Fatalf("keys = %#v", manifest.Keys)
	}
}

func TestReadKeyManifestAcceptsDefaultAndNestedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	override := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := os.WriteFile(path, []byte(`{
  "__default_key": "0x`+key+`",
  "keys": {
    "message/message_0.db": "x'`+override+`'"
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DefaultKey != key {
		t.Fatalf("default key = %q", manifest.DefaultKey)
	}
	if manifest.Keys["message/message_0.db"] != override {
		t.Fatalf("keys = %#v", manifest.Keys)
	}
}

func TestReadKeyManifestRejectsPathTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`{"../escape.db":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKeyManifest(path); err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestReadKeyManifestAcceptsAbsoluteDBStoragePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	dbPath := filepath.Join(string(filepath.Separator), "tmp", "profile", "db_storage", "message", "message_0.db")
	if err := os.WriteFile(path, []byte(`{"`+dbPath+`":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Keys[dbPath] != key {
		t.Fatalf("keys = %#v", manifest.Keys)
	}
}

func TestReadKeyManifestRejectsBadKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	if err := os.WriteFile(path, []byte(`{"message/message_0.db":"not-a-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKeyManifest(path); err == nil {
		t.Fatal("expected bad key error")
	}
}

func TestWriteDefaultKeyManifestFromScan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	written, err := WriteDefaultKeyManifestFromScan([]byte("db key: 0x"+key), path)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected manifest write")
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DefaultKey != key {
		t.Fatalf("default key = %q", manifest.DefaultKey)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %o", info.Mode().Perm())
	}
}

func TestWriteDefaultKeyManifestFromScanAcceptsExistingManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := WriteDefaultKeyManifestFromScan([]byte("manifest already written"), path)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("expected existing manifest to be reused")
	}
}

func TestWriteDefaultKeyManifestFromScanDoesNotOverwriteExistingManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wechat_keys.json")
	perDBKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	stdoutKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	original := `{"keys":{"message/message_0.db":"` + perDBKey + `"}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := WriteDefaultKeyManifestFromScan([]byte("db key: "+stdoutKey), path)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("expected existing manifest to be reused")
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DefaultKey != "" || manifest.Keys["message/message_0.db"] != perDBKey {
		t.Fatalf("manifest was overwritten: %#v", manifest)
	}
}

func TestWriteDefaultKeyManifestFromScanPreservesStdoutManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wechat_keys.json")
	perDBKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	stdout := []byte(`{"keys":{"message/message_0.db":"` + perDBKey + `"}}`)
	written, err := WriteDefaultKeyManifestFromScan(stdout, path)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected stdout manifest write")
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DefaultKey != "" || manifest.Keys["message/message_0.db"] != perDBKey {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestWriteDefaultKeyManifestFromScanExtractsEmbeddedStdoutManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wechat_keys.json")
	perDBKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	stdout := []byte(`[*] scanning WeChat
{"event":"not a manifest"}
{"keys":{"message/message_0.db":"` + perDBKey + `"}}
[*] done`)
	written, err := WriteDefaultKeyManifestFromScan(stdout, path)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected embedded stdout manifest write")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "[*]") || strings.Contains(string(bytes), "not a manifest") {
		t.Fatalf("manifest includes logs: %s", bytes)
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Keys["message/message_0.db"] != perDBKey {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestBuildKeyScanPlanUsesPythonOnlyForPythonScripts(t *testing.T) {
	plan, err := BuildKeyScanPlan(true, false, "/tmp/find_key_memscan.py", "keys.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Command) != 2 || plan.Command[0] != "python3" || plan.Command[1] != "/tmp/find_key_memscan.py" {
		t.Fatalf("python command = %#v", plan.Command)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "per-database keys") {
		t.Fatalf("plan notes = %#v", plan.Notes)
	}
	plan, err = BuildKeyScanPlan(true, false, "/tmp/find_all_keys_macos", "keys.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Command) != 1 || plan.Command[0] != "/tmp/find_all_keys_macos" {
		t.Fatalf("executable command = %#v", plan.Command)
	}
}

func TestExecuteKeyScanSetsManifestEnvironment(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scanner")
	outPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s' "$WEICRAWL_SCAN_OUT"
test "$WEICRAWL_SCAN_OUT" = "$WEICRAWL_KEY_MANIFEST"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := ExecuteKeyScan(context.Background(), KeyScanPlan{Allowed: true, Command: []string{script}, OutputPath: outPath})
	if err != nil {
		t.Fatalf("execute scan: %v\n%s", err, out)
	}
	if string(out) != outPath {
		t.Fatalf("env output = %q", string(out))
	}
}

func TestDecryptSnapshotWithSQLCipherFixture(t *testing.T) {
	sqlcipher, err := FindSQLCipher("")
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotDB := filepath.Join(root, "snapshot", "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain.db")
	createPlainNativeDB(t, plain)
	encryptFixtureDB(t, sqlcipher, plain, snapshotDB, key)
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"message/message_0.db":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "decrypted")
	result, err := DecryptSnapshot(context.Background(), DecryptOptions{
		SnapshotDir: filepath.Join(root, "snapshot"),
		OutputDir:   outDir,
		KeysPath:    keysPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decrypted) != 1 {
		t.Fatalf("decrypted = %#v", result.Decrypted)
	}
	db, err := sql.Open("sqlite", filepath.Join(outDir, "message", "message_0.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(context.Background(), `select count(*) from Name2Id`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("Name2Id count = %d", count)
	}
}

func TestProbeSnapshotKeysWithSQLCipherFixture(t *testing.T) {
	sqlcipher, err := FindSQLCipher("")
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotDB := filepath.Join(root, "snapshot", "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain.db")
	createPlainNativeDB(t, plain)
	encryptFixtureDB(t, sqlcipher, plain, snapshotDB, key)
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	check, err := ProbeSnapshotKeys(context.Background(), DecryptOptions{
		SnapshotDir: filepath.Join(root, "snapshot"),
		KeysPath:    keysPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Ready || !check.ProbeReady || len(check.Probed) != 1 || len(check.ProbeFailed) != 0 {
		t.Fatalf("check = %#v", check)
	}
}

func TestProbeSnapshotKeysReportsBadKey(t *testing.T) {
	sqlcipher, err := FindSQLCipher("")
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	badKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	snapshotDB := filepath.Join(root, "snapshot", "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain.db")
	createPlainNativeDB(t, plain)
	encryptFixtureDB(t, sqlcipher, plain, snapshotDB, key)
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+badKey+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	check, err := ProbeSnapshotKeys(context.Background(), DecryptOptions{
		SnapshotDir: filepath.Join(root, "snapshot"),
		KeysPath:    keysPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Ready || check.ProbeReady || len(check.Probed) != 0 || len(check.ProbeFailed) != 1 {
		t.Fatalf("check = %#v", check)
	}
}

func TestDecryptSnapshotWithDefaultKey(t *testing.T) {
	sqlcipher, err := FindSQLCipher("")
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, rel := range []string{"message/message_0.db", "contact/contact.db"} {
		snapshotDB := filepath.Join(root, "snapshot", "db_storage", rel)
		if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
			t.Fatal(err)
		}
		plain := filepath.Join(root, strings.ReplaceAll(rel, "/", "-"))
		createPlainNativeDB(t, plain)
		encryptFixtureDB(t, sqlcipher, plain, snapshotDB, key)
	}
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "decrypted")
	result, err := DecryptSnapshot(context.Background(), DecryptOptions{
		SnapshotDir: filepath.Join(root, "snapshot"),
		OutputDir:   outDir,
		KeysPath:    keysPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decrypted) != 2 {
		t.Fatalf("decrypted = %#v", result.Decrypted)
	}
}

func TestCheckSnapshotKeysDoesNotDecrypt(t *testing.T) {
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotDB := filepath.Join(root, "snapshot", "db_storage", "message", "message_0.db")
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
	check, err := CheckSnapshotKeys(DecryptOptions{
		SnapshotDir:   filepath.Join(root, "snapshot"),
		OutputDir:     outDir,
		KeysPath:      keysPath,
		SQLCipherPath: sqlcipher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Ready || check.KeyCount != 1 || len(check.Found) != 1 || len(check.Missing) != 0 {
		t.Fatalf("check = %#v", check)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created output dir: %v", err)
	}
}

func TestCheckSnapshotKeysExpandsDefaultKey(t *testing.T) {
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, rel := range []string{"message/message_0.db", "contact/contact.db"} {
		snapshotDB := filepath.Join(root, "snapshot", "db_storage", rel)
		if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapshotDB, []byte("encrypted"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"__default_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sqlcipher := filepath.Join(root, "sqlcipher")
	if err := os.WriteFile(sqlcipher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	check, err := CheckSnapshotKeys(DecryptOptions{
		SnapshotDir:   filepath.Join(root, "snapshot"),
		KeysPath:      keysPath,
		SQLCipherPath: sqlcipher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Ready || !check.DefaultKey || check.KeyCount != 2 || len(check.Found) != 2 {
		t.Fatalf("check = %#v", check)
	}
}

func TestCheckSnapshotKeysResolvesAbsoluteDBStoragePaths(t *testing.T) {
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotRoot := filepath.Join(root, "snapshot")
	snapshotDB := filepath.Join(snapshotRoot, "db_storage", "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(snapshotDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotDB, []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(root, "live", "db_storage", "message", "message_0.db")
	keysPath := filepath.Join(root, "wechat_keys.json")
	if err := os.WriteFile(keysPath, []byte(`{"`+livePath+`":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sqlcipher := filepath.Join(root, "sqlcipher")
	if err := os.WriteFile(sqlcipher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	check, err := CheckSnapshotKeys(DecryptOptions{
		SnapshotDir:   snapshotRoot,
		KeysPath:      keysPath,
		SQLCipherPath: sqlcipher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !check.Ready || check.KeyCount != 1 || len(check.Found) != 1 || check.Found[0].Database != "message/message_0.db" {
		t.Fatalf("check = %#v", check)
	}
}

func createPlainNativeDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`create table Name2Id(user_name text);
insert into Name2Id values('alice');`)
	if err != nil {
		t.Fatal(err)
	}
}

func encryptFixtureDB(t *testing.T, sqlcipher, src, dst, key string) {
	t.Helper()
	commands := `ATTACH DATABASE '` + dst + `' AS encrypted KEY "x'` + key + `'";
SELECT sqlcipher_export('encrypted');
DETACH DATABASE encrypted;
`
	cmd := exec.Command(sqlcipher, src)
	cmd.Stdin = strings.NewReader(commands)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("encrypt fixture: %v\n%s", err, out)
	}
}
