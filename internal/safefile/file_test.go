package safefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsentSidecarAliasUsesDestinationVolume(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "archive.db")
	if err := os.WriteFile(archive, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "ARCHIVE.DB"))
	folded := err == nil
	if folded {
		original, err := os.Stat(archive)
		if err != nil || !os.SameFile(info, original) {
			t.Fatal("case variant is not the same file")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, sidecar := range []string{"-wal", "-shm", "-journal"} {
		protected := archive + sidecar
		output := filepath.Join(root, strings.ToUpper(filepath.Base(protected)))
		err := RejectAliases(output, archive, protected)
		if (err != nil) != folded {
			t.Fatalf("case-folded volume=%v alias=%s error=%v", folded, sidecar, err)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("candidate output created: %v", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("probe files retained: %v %v", entries, err)
	}
	if !folded {
		t.Log("case-sensitive volume exercised; native case-folded rejection not run")
	}
}
