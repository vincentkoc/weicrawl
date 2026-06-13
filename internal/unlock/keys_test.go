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

func TestReadKeyManifestRejectsBadKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	if err := os.WriteFile(path, []byte(`{"message/message_0.db":"not-a-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKeyManifest(path); err == nil {
		t.Fatal("expected bad key error")
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
