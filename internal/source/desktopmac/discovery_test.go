package desktopmac

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsProfilesDatabasesAndMediaDirs(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "container")
	profileRoot := filepath.Join(container, XWeChatRelativeRoot, "wxid_fixture_abcd")
	dbDir := filepath.Join(profileRoot, "db_storage", "message")
	for _, path := range []string{
		filepath.Join(dbDir, "message_0.db-wal"),
		filepath.Join(profileRoot, "msg", "file", "2026-06", "sample.txt"),
		filepath.Join(container, XWeChatRelativeRoot, "Backup", "placeholder"),
		filepath.Join(container, XWeChatRelativeRoot, "all_users", "login", "wxid_fixture", "key_info.db-wal"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dbDir, "message_0.db"), append([]byte("SQLite format 3\x00"), []byte("fixture")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "message_1.db"), []byte("encrypted-ish"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyInfoDB := filepath.Join(container, XWeChatRelativeRoot, "all_users", "login", "wxid_fixture", "key_info.db")
	if err := os.WriteFile(keyInfoDB, append([]byte("SQLite format 3\x00"), []byte("key-info")...), 0o600); err != nil {
		t.Fatal(err)
	}
	disc := Discover(t.Context(), container)
	if !disc.ContainerPresent {
		t.Fatalf("container not present: %#v", disc)
	}
	if len(disc.ProfileRoots) != 1 {
		t.Fatalf("profiles = %#v", disc.ProfileRoots)
	}
	profile := disc.ProfileRoots[0]
	if profile.Wxid != "wxid_fixture" {
		t.Fatalf("wxid = %q", profile.Wxid)
	}
	if len(profile.Databases) != 2 || profile.Databases[0].Role != "message" || len(profile.Databases[0].Sidecars) != 1 {
		t.Fatalf("databases = %#v", profile.Databases)
	}
	if !profile.Databases[0].SQLite || profile.Databases[0].Encrypted {
		t.Fatalf("sqlite header classification = %#v", profile.Databases[0])
	}
	if !profile.Databases[1].Encrypted || disc.EncryptedDBCount != 1 {
		t.Fatalf("encrypted classification = %#v count=%d", profile.Databases[1], disc.EncryptedDBCount)
	}
	if len(profile.MediaDirs) != 1 {
		t.Fatalf("media dirs = %#v", profile.MediaDirs)
	}
	if len(disc.BackupDirs) != 1 {
		t.Fatalf("backup dirs = %#v", disc.BackupDirs)
	}
	if disc.KeyInfoDBCount != 1 || len(disc.KeyInfoDatabases) != 1 || disc.KeyInfoDatabases[0].Role != "key_info" || len(disc.KeyInfoDatabases[0].Sidecars) != 1 {
		t.Fatalf("key info dbs = %#v count=%d", disc.KeyInfoDatabases, disc.KeyInfoDBCount)
	}
}
