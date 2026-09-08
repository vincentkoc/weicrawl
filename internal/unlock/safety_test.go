package unlock

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafetyDecryptRejectsSourceOverlapAndExistingOutput(t *testing.T) {
	root := t.TempDir()
	snapshot := filepath.Join(root, "snapshot")
	dbRoot := filepath.Join(snapshot, "db_storage")
	if err := os.MkdirAll(dbRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dbRoot, "message.db")
	original := []byte("encrypted fixture")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(root, "keys.json")
	if err := os.WriteFile(keys, []byte(`{"__default_key":"`+strings.Repeat("a", 64)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(snapshot, alias); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(existing, "sentinel")
	if err := os.WriteFile(sentinel, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{dbRoot, filepath.Join(alias, "db_storage"), snapshot, existing} {
		_, err := DecryptSnapshot(context.Background(), DecryptOptions{
			SnapshotDir: snapshot, OutputDir: out, KeysPath: keys, SQLCipherPath: "/usr/bin/false",
			RequireOwnedOutput: true,
		})
		if err == nil {
			t.Fatalf("accepted unsafe output %s", out)
		}
		data, _ := os.ReadFile(source)
		if !bytes.Equal(data, original) {
			t.Fatal("source changed")
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "prior.db")
	if err := os.WriteFile(dst, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := decryptOne(context.Background(), "/usr/bin/false", source, dst, strings.Repeat("a", 64)); err == nil {
		t.Fatal("failed cipher accepted")
	}
	data, _ := os.ReadFile(dst)
	if !bytes.Equal(data, original) {
		t.Fatal("failed cipher destroyed prior output")
	}
}

func TestSafetyKeyManifestReplacementIsPrivate(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{true: "helper-output", false: "placeholder"}[valid], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keys.json")
			data := []byte("placeholder")
			if valid {
				data = []byte(`{"__default_key":"` + strings.Repeat("a", 64) + `"}`)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteDefaultKeyManifestFromScan([]byte(strings.Repeat("b", 64)), path); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("key mode %v", info.Mode())
			}
			alias := path + ".link"
			if err := os.Symlink(path, alias); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteDefaultKeyManifestFromScan(nil, alias); err == nil {
				t.Fatal("accepted symlinked key output")
			}
		})
	}
}
