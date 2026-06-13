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
		filepath.Join(dbDir, "message_0.db"),
		filepath.Join(dbDir, "message_0.db-wal"),
		filepath.Join(profileRoot, "msg", "file", "2026-06", "sample.txt"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
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
	if len(profile.Databases) != 1 || profile.Databases[0].Role != "message" || len(profile.Databases[0].Sidecars) != 1 {
		t.Fatalf("databases = %#v", profile.Databases)
	}
	if len(profile.MediaDirs) != 1 {
		t.Fatalf("media dirs = %#v", profile.MediaDirs)
	}
}
