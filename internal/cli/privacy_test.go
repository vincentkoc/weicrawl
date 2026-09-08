package cli

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/source/backup"
	"github.com/vincentkoc/weicrawl/internal/source/importer"
)

func TestCredentialStoresAndTablesStayOutOfContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "backup")
	ordinary := filepath.Join(source, "message_0.db")
	createNativeMessageDB(t, ordinary, "alice")
	db, err := sql.Open("sqlite", ordinary)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table LoGiNkEyInFoTaBlE(id text, key_info_data text);
		insert into LoGiNkEyInFoTaBlE values('fixture','redacted-login');
		create table AUTH_CACHE(id text, payload text);
		insert into AUTH_CACHE values('fixture','redacted-auth');
		update NativeExtra set body='ordinary unknown content';`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	// Typed fixture content is also excluded when the whole store is recognized.
	createFixtureDB(t, filepath.Join(source, "KeY_InFo.db"))
	arc, err := archive.Open(t.Context(), filepath.Join(root, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	result, err := backup.Sync(t.Context(), arc, backup.Options{Root: source, ProfileID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedMessages != 3 || result.ImportedRawRecords != 1 {
		t.Fatalf("unexpected imported content: %#v", result)
	}
	for _, table := range []string{"LoginKeyInfoTABLE", "AuTh_CaChE"} {
		if err := arc.InsertRawRecord(t.Context(), "fixture", "backup", table, "legacy", "unsupported",
			map[string]any{"source_table": "NativeExtra", "source_db": "ordinary.db", "body": "redacted-legacy"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := arc.InsertRawRecord(t.Context(), "fixture", "backup", "NativeExtra", "legacy", "unsupported",
		map[string]any{"source_db": "key_info.db", "body": "preserved legacy content"}); err != nil {
		t.Fatal(err)
	}
	e := env{ctx: t.Context(), out: &bytes.Buffer{}}
	for _, scope := range []string{"all", "raw"} {
		path := filepath.Join(root, scope+".jsonl")
		if err := e.exportJSONL(arc, path, scope); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "redacted-") || !strings.Contains(text, "ordinary unknown content") ||
			!strings.Contains(text, "preserved legacy content") {
			t.Fatalf("%s export violated credential-table policy", scope)
		}
	}
	var rawCount int
	if err := arc.DB().QueryRow("select count(*) from raw_records").Scan(&rawCount); err != nil || rawCount != 4 {
		t.Fatalf("legacy records changed: count=%d error=%v", rawCount, err)
	}
}

func TestRecognizedCredentialStoreIsExcludedBeforeOpening(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "KEY_INFO.DB")
	if err := os.WriteFile(path, []byte("not a readable database"), 0o600); err != nil {
		t.Fatal(err)
	}
	arc, err := archive.Open(t.Context(), filepath.Join(root, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	result, warnings, err := importer.ImportFixtureDatabases(t.Context(), arc, "fixture",
		[]importer.File{{Path: path, Role: "backup"}})
	if err != nil || result != (importer.Result{}) || len(warnings) != 1 {
		t.Fatalf("metadata-only exclusion: result=%#v warnings=%v error=%v", result, warnings, err)
	}
}
