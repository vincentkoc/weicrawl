package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincentkoc/weicrawl/internal/archive"
)

func TestSafetyExportArchiveAliasesAndPrivateReplacement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "archive.db")
	arc, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	e := env{ctx: ctx, out: &bytes.Buffer{}}
	aliases := []string{path, path + "-wal", path + "-shm", path + "-journal"}
	for _, kind := range []string{"symlink", "hardlink"} {
		alias := filepath.Join(root, kind)
		if kind == "symlink" {
			err = os.Symlink(path, alias)
		} else {
			err = os.Link(path, alias)
		}
		if err != nil {
			t.Fatal(err)
		}
		aliases = append(aliases, alias)
	}
	for _, alias := range aliases {
		if err := e.exportJSONL(arc, alias, "all"); err == nil {
			t.Fatalf("accepted archive alias %s", alias)
		}
	}
	if _, err := arc.Status(ctx); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(root, "export.jsonl")
	if err := os.WriteFile(export, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.exportJSONL(arc, export, "all"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(export)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode %v", info.Mode())
	}
	before, _ := os.ReadFile(export)
	_ = arc.Close()
	if err := e.exportJSONL(arc, export, "all"); err == nil {
		t.Fatal("closed DB export succeeded")
	}
	after, _ := os.ReadFile(export)
	if !bytes.Equal(before, after) {
		t.Fatal("failed export replaced previous output")
	}
}

func TestSafetyObservationalCommandsDoNotCreateOrMigrate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	path := filepath.Join(root, "absent.db")
	for _, args := range [][]string{{"status"}, {"doctor"}, {"sql", "select 1"}, {"search", "hello"}, {"tui", "--json"}} {
		runForTest(append([]string{"--json", "--db", path}, args...)...)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%v created absent archive: %v", args, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("create table provider_source(secret text)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{{"status"}, {"doctor"}, {"sql", "select 1"}, {"search", "hello"}} {
		runForTest(append([]string{"--json", "--db", path}, args...)...)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("observational route changed foreign database")
	}
}

func TestSafetyMarkdownProfilesFieldsAndCollisions(t *testing.T) {
	ctx := context.Background()
	arc, err := archive.Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	for i, item := range [][2]string{{"profile-a", "a/b"}, {"profile-b", "a/b"}, {"profile-a", "a:b"}} {
		if err := arc.UpsertMessage(ctx, archive.Message{
			ProfileID: item[0], MessageID: string(rune('a' + i)), ChatID: item[1],
			SenderID: "sender", SentAt: "2026-09-08T00:00:00Z", Text: item[0],
			MessageType: "text",
		}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	e := env{ctx: ctx, out: &bytes.Buffer{}}
	if err := e.exportMarkdown(arc, dir); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	if len(files) != 3 {
		t.Fatalf("got %d files", len(files))
	}
	for _, file := range files {
		data, _ := os.ReadFile(file)
		if !strings.Contains(string(data), "**sender**") || !strings.Contains(string(data), "2026-09-08T00:00:00Z") {
			t.Fatalf("missing sender/time: %s", data)
		}
	}
}

func TestSafetyStatusUsesLastSuccessfulRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	path := filepath.Join(root, "archive.db")
	arc, err := archive.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []archive.SyncRun{
		{RunID: "good", Source: "fixture", StartedAt: "2026-09-07T00:00:00Z", FinishedAt: "2026-09-07T01:00:00Z", Status: "success"},
		{RunID: "bad", Source: "fixture", StartedAt: "2026-09-08T00:00:00Z", FinishedAt: "2026-09-08T01:00:00Z", Status: "partial", Warnings: []string{"shard failed"}},
	} {
		if err := arc.InsertSyncRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	_ = arc.Close()
	code, out, stderr := runForTest("--json", "--db", path, "status")
	if code != 0 {
		t.Fatalf("status: %s", stderr)
	}
	var result struct {
		Control struct {
			State      string   `json:"state"`
			LastSyncAt string   `json:"last_sync_at"`
			Warnings   []string `json:"warnings"`
		} `json:"control"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Control.State != "degraded" || result.Control.LastSyncAt != "2026-09-07T01:00:00Z" {
		t.Fatalf("status: %s", out)
	}
}

func TestSafetyReadOnlySQLBatchCannotChangeArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	arc, err := archive.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = arc.Close()
	code, _, _ := runForTest("--json", "--db", path, "sql", "select 1; pragma user_version=73;")
	if code == 0 {
		t.Fatal("mutating batch succeeded")
	}
	ro, err := archive.OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var version int
	if err := ro.DB().QueryRow("pragma user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("user_version=%d error=%v", version, err)
	}
}

func TestSafetyUnlockRejectsArchiveInsideOwnedOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	output := filepath.Join(root, "decrypted")
	code, _, _ := runForTest("--json", "--db", filepath.Join(output, "archive.db"),
		"unlock", "desktop", "--sync", "--keys", filepath.Join(root, "keys.json"),
		"--snapshot", filepath.Join(root, "snapshot"), "--out", output)
	if code == 0 {
		t.Fatal("archive inside cleanup root accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("output created before overlap check: %v", err)
	}
}

func TestSafetyPartialShardImportIsNotHealthy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	source := filepath.Join(root, "decrypted")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	createNativeMessageDB(t, filepath.Join(source, "message_0.db"), "alice")
	if err := os.WriteFile(filepath.Join(source, "message_1.db"), []byte("corrupt database"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "archive.db")
	code, _, _ := runForTest("--json", "--db", path, "sync", "--source", "desktop-macos",
		"--profile", "fixture", "--decrypted-dir", source)
	if code == 0 {
		t.Fatal("partial import reported success")
	}
	arc, err := archive.OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer arc.Close()
	status, err := arc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.MessageCount != 3 || status.LastSyncRun == nil || status.LastSyncRun.Status != "partial" {
		t.Fatalf("partial state lost: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(source, "message_0.db")); err != nil {
		t.Fatal("incomplete import lost recovery material")
	}
}

func TestSafetyUnlockOwnedCleanupAndIncompleteImport(t *testing.T) {
	for _, scenario := range []string{"complete", "keep", "partial", "ambiguous", "existing", "existing-keep", "existing-no-sync", "case-folded", "dot-parent-alias"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if scenario == "case-folded" {
				if err := os.WriteFile(filepath.Join(root, "CaseProbe"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(root, "caseprobe")); os.IsNotExist(err) {
					t.Skip("requires a case-insensitive filesystem")
				}
			}
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
			plain := filepath.Join(root, "plain.db")
			createNativeMessageDB(t, plain, "alice")
			if scenario == "partial" {
				db, err := sql.Open("sqlite", plain)
				if err != nil {
					t.Fatal(err)
				}
				// A readable SQLite database with a supported but malformed native table.
				if _, err := db.Exec(`alter table "` + nativeMsgTable("alice") + `" rename column message_content to unsupported_content`); err != nil {
					t.Fatal(err)
				}
				_ = db.Close()
			}
			snapshot := filepath.Join(root, "snapshot")
			if err := os.MkdirAll(filepath.Join(snapshot, "db_storage"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(snapshot, "db_storage", "message.db"), []byte("synthetic ciphertext"), 0o600); err != nil {
				t.Fatal(err)
			}
			keys := filepath.Join(root, "keys.json")
			if err := os.WriteFile(keys, []byte(`{"__default_key":"`+strings.Repeat("a", 64)+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cipher := filepath.Join(root, "fixture-cipher")
			t.Setenv("FIXTURE_PLAINTEXT", plain)
			// This is an output-producing process double, not SQLCipher/native proof.
			script := "#!/bin/sh\nout=$(sed -n \"s/^ATTACH DATABASE '\\(.*\\)' AS plaintext KEY '';$/\\1/p\")\ncp \"$FIXTURE_PLAINTEXT\" \"$out\"\n"
			if err := os.WriteFile(cipher, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, "decrypted")
			dbPath := filepath.Join(root, "archive.db")
			if scenario == "ambiguous" {
				arc, err := archive.Open(t.Context(), dbPath)
				if err != nil {
					t.Fatal(err)
				}
				legacy := legacyNativeMessage("message.db", 9)
				legacy.ProfileID = "decrypted"
				legacy.MessageID = "unknown:" + legacy.SourceRowID
				legacy.SourceDB = "unknown"
				legacy.RawJSON = "{}"
				if err := arc.UpsertMessage(t.Context(), legacy); err != nil {
					t.Fatal(err)
				}
				if err := arc.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "case-folded" {
				out = filepath.Join(root, "Decrypted")
				dbPath = filepath.Join(root, "decrypted", "archive.db")
			}
			if scenario == "dot-parent-alias" {
				target := filepath.Join(root, "target", "nested")
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(root, "alias")
				if err := os.Symlink(target, alias); err != nil {
					t.Fatal(err)
				}
				out = alias + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "decrypted"
				dbPath = filepath.Join(root, "target", "decrypted", "archive.db")
			}
			if strings.HasPrefix(scenario, "existing") {
				if err := os.Mkdir(out, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(out, "sentinel"), []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--json", "--db", dbPath, "unlock", "desktop",
				"--keys", keys, "--snapshot", snapshot, "--out", out, "--sqlcipher", cipher}
			if scenario != "existing-no-sync" {
				args = append(args, "--sync")
			}
			if scenario == "keep" || scenario == "existing-keep" {
				args = append(args, "--keep-decrypted-snapshot")
			}
			code, stdout, stderr := runForTest(args...)
			if scenario == "complete" || scenario == "keep" || scenario == "existing-keep" || scenario == "existing-no-sync" {
				if code != 0 {
					t.Fatalf("code=%d out=%s error=%s", code, stdout, stderr)
				}
			} else if code == 0 {
				t.Fatalf("incomplete/unsafe operation succeeded: %s", stdout)
			}
			_, err := os.Stat(out)
			if scenario == "complete" && !os.IsNotExist(err) {
				t.Fatalf("owned output retained: %v", err)
			}
			if scenario != "complete" && scenario != "dot-parent-alias" && err != nil {
				t.Fatalf("recovery/kept output removed: %v", err)
			}
			if scenario == "dot-parent-alias" && !os.IsNotExist(err) {
				t.Fatalf("aliased root created before overlap check: %v", err)
			}
			if strings.HasPrefix(scenario, "existing") {
				data, _ := os.ReadFile(filepath.Join(out, "sentinel"))
				if string(data) != "untouched" {
					t.Fatal("caller-owned contents changed")
				}
			}
			if scenario == "case-folded" || scenario == "dot-parent-alias" {
				if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
					t.Fatalf("archive created inside cleanup root: %v", err)
				}
			}
			if scenario == "ambiguous" {
				arc, err := archive.OpenReadOnly(t.Context(), dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer arc.Close()
				status, err := arc.Status(t.Context())
				if err != nil || status.MessageCount != 1 || status.LastSyncRun == nil || status.LastSyncRun.Status != "partial" {
					t.Fatalf("ambiguous import lost preflight/partial state: %#v error=%v", status, err)
				}
				data, err := os.ReadFile(filepath.Join(out, "message.db"))
				if err != nil || !bytes.HasPrefix(data, []byte("SQLite format 3")) {
					t.Fatalf("ambiguity lost decrypted recovery material: %v", err)
				}
			}
		})
	}
}
