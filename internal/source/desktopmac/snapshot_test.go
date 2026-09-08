package desktopmac

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vincentkoc/weicrawl/internal/archive"
)

func TestSafetyFailedSnapshotCleanupAndCancellation(t *testing.T) {
	for _, keep := range []bool{false, true} {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		dbPath := filepath.Join(source, "db_storage", "first.db")
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dbPath, []byte("private copy"), 0o600); err != nil {
			t.Fatal(err)
		}
		opts := SnapshotOptions{
			CacheDir: filepath.Join(root, "cache"), Keep: keep,
			Profile: Profile{ProfileID: "fixture", Root: source, Databases: []DBFile{
				{Path: dbPath, Role: "message"}, {Path: filepath.Join(filepath.Dir(dbPath), "missing.db"), Role: "message"},
			}},
		}
		_, err := CreateSnapshot(context.Background(), opts)
		if err == nil {
			t.Fatal("incomplete copy succeeded")
		}
		dirs, _ := os.ReadDir(filepath.Join(opts.CacheDir, "snapshots"))
		if (len(dirs) > 0) != keep {
			t.Fatalf("keep=%v retained=%d", keep, len(dirs))
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := CreateSnapshot(ctx, opts); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled snapshot: %v", err)
		}
	}
}

func TestSafetyArchiveFailureCleansOwnedSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	arc, err := archive.Open(context.Background(), filepath.Join(root, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = arc.Close()
	cache := filepath.Join(root, "cache")
	_, err = SyncDesktopSnapshot(context.Background(), arc, SnapshotOptions{
		CacheDir: cache, Profile: Profile{ProfileID: "fixture", Root: source},
	})
	if err == nil {
		t.Fatal("closed archive accepted")
	}
	dirs, _ := os.ReadDir(filepath.Join(cache, "snapshots"))
	if len(dirs) != 0 {
		t.Fatalf("abandoned copies: %v", dirs)
	}
}

func TestSafetyDotProfileCannotRemoveSiblingSnapshots(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	retained := filepath.Join(cache, "snapshots", "retained", "message.db")
	if err := os.MkdirAll(filepath.Dir(retained), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retained, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	arc, err := archive.Open(t.Context(), filepath.Join(root, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	if _, err := SyncDesktopSnapshot(t.Context(), arc, SnapshotOptions{
		CacheDir: cache, Profile: Profile{ProfileID: ".", Root: root},
	}); err == nil {
		t.Fatal("dot profile accepted")
	}
	data, err := os.ReadFile(retained)
	if err != nil || string(data) != "retained" {
		t.Fatalf("sibling snapshot changed: %q %v", data, err)
	}
}

func TestSafetyCleanupFailurePersistsPartialRun(t *testing.T) {
	root := t.TempDir()
	arc, err := archive.Open(t.Context(), filepath.Join(root, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	attempts := 0
	result, err := syncDesktopSnapshot(t.Context(), arc, SnapshotOptions{
		CacheDir: filepath.Join(root, "cache"), Profile: Profile{ProfileID: "fixture", Root: root},
	}, func(string) error {
		attempts++
		return errors.New("synthetic cleanup failure")
	})
	if err == nil || result.Status != "partial" || attempts != 1 {
		t.Fatalf("result=%#v attempts=%d error=%v", result, attempts, err)
	}
	run, err := arc.LastSyncRun(t.Context())
	if err != nil || run == nil || run.Status != "partial" || run.SnapshotPath == "" {
		t.Fatalf("stored run=%#v error=%v", run, err)
	}
	success, err := arc.LastSuccessfulSyncAt(t.Context())
	if err != nil || success != "" {
		t.Fatalf("cleanup failure advanced successful freshness: %q %v", success, err)
	}
}

func TestSafetyHashHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.db")
	if err := os.WriteFile(path, make([]byte, 65536), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fileSHA256(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash: %v", err)
	}
}
