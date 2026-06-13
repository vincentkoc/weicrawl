package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/snapshot"
	cktui "github.com/openclaw/crawlkit/tui"
	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/config"
	"github.com/vincentkoc/weicrawl/internal/schema"
	"github.com/vincentkoc/weicrawl/internal/source/backup"
	"github.com/vincentkoc/weicrawl/internal/source/desktopmac"
	"github.com/vincentkoc/weicrawl/internal/source/officialaccount"
	"github.com/vincentkoc/weicrawl/internal/unlock"
	"github.com/vincentkoc/weicrawl/internal/version"
)

type globals struct {
	configPath string
	dbPath     string
	profile    string
	json       bool
	quiet      bool
	verbose    bool
}

type env struct {
	ctx    context.Context
	out    io.Writer
	errOut io.Writer
	global globals
	loaded config.Loaded
	format output.Format
}

func Main(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	global, rest, err := parseGlobals(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	format, err := output.Resolve("", global.json)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(rest) == 0 {
		rest = []string{"help"}
	}
	loaded, err := config.Load(global.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if strings.TrimSpace(global.dbPath) != "" {
		loaded.Config.Archive.DBPath = config.Expand(global.dbPath)
	}
	e := env{ctx: ctx, out: stdout, errOut: stderr, global: global, loaded: loaded, format: format}
	if err := e.run(rest); err != nil {
		if !global.quiet {
			fmt.Fprintln(stderr, err)
		}
		if output.IsUsage(err) {
			return 2
		}
		return 1
	}
	return 0
}

func (e env) run(args []string) error {
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "help", "-h", "--help":
		return e.write("help", usage())
	case "version":
		return e.write("version", map[string]string{"app": version.AppName, "version": version.Version, "commit": version.Commit})
	case "init":
		return e.runInit(rest)
	case "doctor":
		return e.runDoctor(rest)
	case "metadata":
		return e.write("metadata", manifest())
	case "status":
		return e.runStatus()
	case "sync":
		return e.runSync(rest)
	case "unlock":
		return e.runUnlock(rest)
	case "profiles":
		return e.runList("profiles", rest)
	case "contacts":
		return e.runList("contacts", rest)
	case "chats":
		return e.runList("chats", rest)
	case "messages":
		return e.runList("messages", rest)
	case "favorites":
		return e.runList("favorites", rest)
	case "articles":
		return e.runList("biz_articles", rest)
	case "media":
		return e.runList("media_items", rest)
	case "runs":
		return e.runList("sync_runs", rest)
	case "search":
		return e.runSearch(rest)
	case "sql":
		return e.runSQL(rest)
	case "snapshot":
		return e.runSnapshot(rest)
	case "import":
		return e.runSnapshotImport(rest)
	case "export":
		return e.runExport(rest)
	case "tui":
		return e.runTUI(rest)
	case "completion":
		return e.runCompletion(rest)
	default:
		return output.UsageError{Err: fmt.Errorf("unknown command %q", cmd)}
	}
}

func (e env) runInit(args []string) error {
	fs := newFlagSet("init")
	overwrite := fs.Bool("overwrite", false, "overwrite existing config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	loaded, created, err := config.WriteDefault(e.global.configPath, *overwrite)
	if err != nil {
		return err
	}
	arc, err := archive.Open(e.ctx, loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	return e.write("init", map[string]any{
		"config_path":    loaded.Path,
		"config_created": created,
		"database_path":  arc.Path(),
		"schema_version": schema.Version,
	})
}

func (e env) runDoctor(args []string) error {
	fs := newFlagSet("doctor")
	probeUnlock := fs.Bool("probe-unlock", false, "probe configured unlock path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	arc, dbErr := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	var schemaVersion int
	if dbErr == nil {
		defer arc.Close()
		schemaVersion, _ = arc.SchemaVersion(e.ctx)
	}
	disc := desktopmac.Discover(e.ctx, e.loaded.Config.DesktopMacOS.ContainerPath)
	sqlcipherPath, sqlcipherErr := unlock.FindSQLCipher("")
	checks := []map[string]any{
		{"id": "config_readable", "ok": true, "path": e.loaded.Path},
		{"id": "archive_db_openable", "ok": dbErr == nil, "path": e.loaded.Config.Archive.DBPath, "error": errString(dbErr)},
		{"id": "schema_version", "ok": schemaVersion == schema.Version, "current": schemaVersion, "want": schema.Version},
		{"id": "wechat_app_present", "ok": disc.AppPresent, "path": disc.AppPath, "version": disc.AppVersion},
		{"id": "wechat_container_present", "ok": disc.ContainerPresent, "path": disc.ContainerPath},
		{"id": "profile_discovery", "ok": len(disc.ProfileRoots) > 0, "profiles": len(disc.ProfileRoots)},
		{"id": "database_shards", "ok": disc.DatabaseCount > 0, "count": disc.DatabaseCount},
		{"id": "unlock_configured", "ok": e.loaded.Config.Unlock.AllowProcessInspect || e.loaded.Config.Unlock.AllowKeychain || e.loaded.Config.Unlock.StoreKeychain},
		{"id": "unlock_key_manifest_supported", "ok": true, "method": "unlock desktop --keys <wechat_keys.json> --snapshot <copied-profile-root> --out <decrypted-dir>"},
		{"id": "sqlcipher_available", "ok": sqlcipherErr == nil, "path": sqlcipherPath, "error": errString(sqlcipherErr)},
		{"id": "decrypted_snapshot_retention", "ok": !e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots, "enabled": e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots},
		{"id": "probe_unlock_requested", "ok": !*probeUnlock || sqlcipherErr == nil, "skipped": !*probeUnlock, "note": "probe requires sqlcipher and an external key manifest"},
	}
	return e.write("doctor", map[string]any{
		"state":         "ok",
		"checks":        checks,
		"desktop_macos": disc,
		"warnings":      disc.Warnings,
	})
}

func (e env) runStatus() error {
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	status, err := arc.Status(e.ctx)
	if err != nil {
		return err
	}
	ckStatus := control.NewStatus("weicrawl", "local WeChat archive")
	ckStatus.State = "ok"
	ckStatus.ConfigPath = e.loaded.Path
	ckStatus.DatabasePath = arc.Path()
	ckStatus.Databases = []control.Database{control.SQLiteDatabase("archive", "weicrawl archive", "archive", arc.Path(), true, []control.Count{
		control.NewCount("profiles", "Profiles", status.ProfileCount),
		control.NewCount("contacts", "Contacts", status.ContactCount),
		control.NewCount("chats", "Chats", status.ChatCount),
		control.NewCount("messages", "Messages", status.MessageCount),
	})}
	ckStatus.Counts = []control.Count{
		control.NewCount("profiles", "Profiles", status.ProfileCount),
		control.NewCount("contacts", "Contacts", status.ContactCount),
		control.NewCount("chats", "Chats", status.ChatCount),
		control.NewCount("messages", "Messages", status.MessageCount),
		control.NewCount("media_items", "Media metadata", status.MediaCount),
		control.NewCount("favorites", "Favorites", status.FavoriteCount),
		control.NewCount("biz_articles", "Public-account articles", status.PublicAccountArticleCount),
		control.NewCount("moments", "Moments", status.MomentCount),
	}
	if status.LastSyncRun != nil {
		ckStatus.LastSyncAt = status.LastSyncRun.FinishedAt
	}
	return e.write("status", map[string]any{
		"control": ckStatus,
		"archive": status,
		"source": map[string]any{
			"desktop_macos": map[string]any{
				"container_path":           e.loaded.Config.DesktopMacOS.ContainerPath,
				"keep_source_snapshots":    e.loaded.Config.DesktopMacOS.KeepSourceSnapshots,
				"keep_decrypted_snapshots": e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots,
			},
		},
	})
}

func (e env) runSync(args []string) error {
	fs := newFlagSet("sync")
	source := fs.String("source", "desktop-macos", "source adapter")
	profileFlag := fs.String("profile", e.global.profile, "profile id or wxid")
	includeMedia := fs.Bool("include-media", false, "copy media metadata sidecars")
	mediaMode := fs.String("media-mode", e.loaded.Config.DesktopMacOS.MediaMode, "metadata or copy")
	keepSource := fs.Bool("keep-source-snapshot", e.loaded.Config.DesktopMacOS.KeepSourceSnapshots, "keep copied source snapshot")
	keepDecrypted := fs.Bool("keep-decrypted-snapshot", e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots, "keep decrypted snapshot")
	decryptedDir := fs.String("decrypted-dir", "", "import a decrypted db_storage tree")
	backupRoot := fs.String("backup-root", "", "user-selected WeChat backup root")
	_ = fs.Bool("full", false, "full sync")
	_ = fs.String("since", "", "lower bound timestamp")
	_ = fs.Int("concurrency", 1, "copy concurrency")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keepDecrypted {
		return errors.New("--keep-decrypted-snapshot is reserved for the future explicit unlock flow")
	}
	if *mediaMode != "" && *mediaMode != "metadata" && *mediaMode != "copy" {
		return output.UsageError{Err: fmt.Errorf("unsupported media mode %q", *mediaMode)}
	}
	if *source != "desktop-macos" && *source != "desktop-backup" && *source != "all" && *source != "official-account-api" {
		return output.UsageError{Err: fmt.Errorf("source %q is not implemented yet", *source)}
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	if *source == "official-account-api" {
		result, err := officialaccount.Sync(e.ctx, arc, officialaccount.Options{Config: e.loaded.Config.OfficialAccount})
		if err != nil {
			return err
		}
		return e.write("sync", result)
	}
	if *source == "desktop-backup" {
		if strings.TrimSpace(*backupRoot) == "" {
			return output.UsageError{Err: errors.New("--backup-root is required for desktop-backup")}
		}
		result, err := backup.Sync(e.ctx, arc, backup.Options{Root: config.Expand(*backupRoot), ProfileID: *profileFlag})
		if err != nil {
			return err
		}
		return e.write("sync", result)
	}
	if strings.TrimSpace(*decryptedDir) != "" {
		profileID := *profileFlag
		if profileID == "" {
			profileID = "decrypted"
		}
		result, err := desktopmac.SyncDecryptedDirectory(e.ctx, arc, profileID, config.Expand(*decryptedDir), "")
		if err != nil {
			return err
		}
		return e.write("sync", result)
	}
	disc := desktopmac.Discover(e.ctx, e.loaded.Config.DesktopMacOS.ContainerPath)
	profile, ok := desktopmac.SelectProfile(disc, *profileFlag)
	if !ok {
		return fmt.Errorf("profile %q not found; discovered %d profiles", *profileFlag, len(disc.ProfileRoots))
	}
	result, err := desktopmac.SyncDesktopSnapshot(e.ctx, arc, desktopmac.SnapshotOptions{
		CacheDir:     e.loaded.Config.Archive.CacheDir,
		Profile:      profile,
		AppVersion:   disc.AppVersion,
		IncludeMedia: *includeMedia,
		MediaMode:    *mediaMode,
		Keep:         *keepSource,
	})
	if err != nil {
		return err
	}
	return e.write("sync", result)
}

func (e env) runUnlock(args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	sub := args[0]
	fs := newFlagSet("unlock " + sub)
	profile := fs.String("profile", e.global.profile, "profile id or wxid")
	keysPath := fs.String("keys", "", "wechat_keys.json path")
	snapshotPath := fs.String("snapshot", "", "copied snapshot profile root")
	outDir := fs.String("out", "", "decrypted output dir")
	sqlcipherPath := fs.String("sqlcipher", "", "sqlcipher binary path")
	allowProcess := fs.Bool("allow-process-inspect", false, "allow process inspection")
	allowKeychain := fs.Bool("allow-keychain", false, "allow keychain access")
	storeKeychain := fs.Bool("store-keychain", false, "persist unlock material in keychain")
	once := fs.Bool("once", false, "memory only")
	explain := fs.Bool("explain", false, "explain planned method")
	script := fs.String("script", "", "key scanner script path")
	outputPath := fs.String("scan-out", "wechat_keys.json", "expected key manifest path")
	execute := fs.Bool("execute", false, "run the key scanner instead of printing the plan")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	disc := desktopmac.Discover(e.ctx, e.loaded.Config.DesktopMacOS.ContainerPath)
	payload := map[string]any{
		"subcommand":  sub,
		"method":      "none",
		"app_version": disc.AppVersion,
		"profile":     *profile,
		"persisted":   false,
		"next":        "run `weicrawl sync --source desktop-macos` after an unlock method is implemented",
	}
	switch sub {
	case "status":
		payload["configured"] = e.loaded.Config.Unlock
		payload["available"] = false
	case "desktop":
		payload["explain"] = *explain
		payload["once"] = *once
		payload["requested"] = map[string]bool{"allow_process_inspect": *allowProcess, "allow_keychain": *allowKeychain, "store_keychain": *storeKeychain}
		if strings.TrimSpace(*keysPath) == "" {
			payload["available"] = false
			payload["warning"] = "process inspection is deliberately not implemented; pass --keys with a reviewed key manifest to decrypt a copied snapshot"
			break
		}
		if strings.TrimSpace(*snapshotPath) == "" || strings.TrimSpace(*outDir) == "" {
			return output.UsageError{Err: errors.New("unlock desktop with --keys requires --snapshot and --out")}
		}
		result, err := unlock.DecryptSnapshot(e.ctx, unlock.DecryptOptions{
			SnapshotDir:   config.Expand(*snapshotPath),
			OutputDir:     config.Expand(*outDir),
			KeysPath:      config.Expand(*keysPath),
			SQLCipherPath: config.Expand(*sqlcipherPath),
		})
		if err != nil {
			return err
		}
		return e.write("unlock", result)
	case "scan-keys":
		plan, err := unlock.BuildKeyScanPlan(*allowProcess, *execute, config.Expand(*script), config.Expand(*outputPath))
		if err != nil {
			return err
		}
		if !*execute {
			return e.write("unlock", plan)
		}
		out, err := unlock.ExecuteKeyScan(e.ctx, plan)
		if err != nil {
			return fmt.Errorf("key scan failed: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		return e.write("unlock", map[string]any{"command": plan.Command, "output": strings.TrimSpace(string(out))})
	case "forget":
		payload["forgotten"] = true
	default:
		return output.UsageError{Err: fmt.Errorf("unknown unlock subcommand %q", sub)}
	}
	return e.write("unlock", payload)
}

func (e env) runList(table string, args []string) error {
	fs := newFlagSet(table)
	limit := fs.Int("limit", 100, "row limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	result, err := arc.List(e.ctx, table, *limit)
	if err != nil {
		return err
	}
	return e.write(table, result)
}

func (e env) runSearch(args []string) error {
	fs := newFlagSet("search")
	chat := fs.String("chat", "", "chat id")
	sender := fs.String("from", "", "sender id")
	kind := fs.String("kind", "", "message type")
	since := fs.String("since", "", "lower bound timestamp, date, or duration like 30d")
	limit := fs.Int("limit", 50, "limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return output.UsageError{Err: errors.New("search query is required")}
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	sinceValue, err := parseSince(*since)
	if err != nil {
		return output.UsageError{Err: err}
	}
	hits, err := arc.SearchMessages(e.ctx, query, *chat, *sender, *kind, sinceValue, *limit)
	if err != nil {
		return err
	}
	return e.write("search", map[string]any{"query": query, "since": sinceValue, "hits": hits})
}

func (e env) runSQL(args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return output.UsageError{Err: errors.New("sql query is required")}
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	result, err := arc.Query(e.ctx, query)
	if err != nil {
		return err
	}
	return e.write("sql", result)
}

func (e env) runSnapshot(args []string) error {
	if len(args) == 0 {
		return output.UsageError{Err: errors.New("snapshot subcommand is required")}
	}
	switch args[0] {
	case "create":
		fs := newFlagSet("snapshot create")
		outDir := fs.String("out", "", "snapshot output dir")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*outDir) == "" {
			return output.UsageError{Err: errors.New("--out is required")}
		}
		arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
		if err != nil {
			return err
		}
		defer arc.Close()
		manifest, err := snapshot.Export(e.ctx, snapshot.ExportOptions{DB: arc.DB(), RootDir: config.Expand(*outDir), Tables: schema.SnapshotTables})
		if err != nil {
			return err
		}
		return e.write("snapshot", manifest)
	case "import":
		return e.runSnapshotImport(args[1:])
	default:
		return output.UsageError{Err: fmt.Errorf("unknown snapshot subcommand %q", args[0])}
	}
}

func (e env) runSnapshotImport(args []string) error {
	fs := newFlagSet("import")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return output.UsageError{Err: errors.New("snapshot path is required")}
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	manifest, err := snapshot.Import(e.ctx, snapshot.ImportOptions{DB: arc.DB(), RootDir: config.Expand(fs.Arg(0)), DeleteTables: schema.SnapshotTables})
	if err != nil {
		return err
	}
	if err := arc.RebuildFTS(e.ctx); err != nil {
		return err
	}
	return e.write("import", manifest)
}

func (e env) runExport(args []string) error {
	fs := newFlagSet("export")
	outPath := fs.String("out", "", "output file")
	format := fs.String("format", "jsonl", "jsonl or markdown")
	scope := fs.String("scope", "all", "messages, articles, favorites, media, moments, raw, or all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) == "" {
		return output.UsageError{Err: errors.New("--out is required")}
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	if *format == "markdown" {
		if *scope != "all" && *scope != "messages" {
			return output.UsageError{Err: fmt.Errorf("markdown export only supports messages scope, got %q", *scope)}
		}
		return e.exportMarkdown(arc, config.Expand(*outPath))
	}
	if *format != "jsonl" {
		return output.UsageError{Err: fmt.Errorf("unsupported export format %q", *format)}
	}
	return e.exportJSONL(arc, config.Expand(*outPath), *scope)
}

type exportQuery struct {
	Entity string
	Query  string
}

func (e env) exportJSONL(arc *archive.Archive, path, scope string) error {
	queries, err := exportQueries(scope)
	if err != nil {
		return output.UsageError{Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	rowsWritten := 0
	counts := map[string]int{}
	for _, export := range queries {
		result, err := arc.Query(e.ctx, export.Query)
		if err != nil {
			_ = file.Close()
			return err
		}
		for _, row := range result.Values {
			row["entity"] = export.Entity
			if err := enc.Encode(row); err != nil {
				_ = file.Close()
				return err
			}
			rowsWritten++
			counts[export.Entity]++
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	return e.write("export", map[string]any{"path": path, "format": "jsonl", "scope": scope, "rows": rowsWritten, "counts": counts})
}

func (e env) exportMarkdown(arc *archive.Archive, dir string) error {
	result, err := arc.Query(e.ctx, `select chat_id, coalesce(sender_id,''), message_type, coalesce(sent_at,''), text from messages order by chat_id, coalesce(sent_at,''), message_id`)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := map[string][]string{}
	for _, row := range result.Values {
		chatID := fmt.Sprint(row["chat_id"])
		line := fmt.Sprintf("- `%s` **%s** [%s]: %s", row["sent_at"], row["sender_id"], row["message_type"], row["text"])
		files[chatID] = append(files[chatID], line)
	}
	for chatID, lines := range files {
		path := filepath.Join(dir, safeMarkdownName(chatID)+".md")
		body := "# " + chatID + "\n\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return e.write("export", map[string]any{"path": dir, "format": "markdown", "files": len(files)})
}

func exportQueries(scope string) ([]exportQuery, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "all"
	}
	all := []exportQuery{
		{Entity: "profile", Query: `select profile_id, wxid, display_name, source_root, app_version, raw_json, updated_at from profiles order by profile_id`},
		{Entity: "contact", Query: `select profile_id, contact_id, alias, display_name, remark_name, kind, avatar_ref, raw_json, updated_at from contacts order by profile_id, contact_id`},
		{Entity: "chat", Query: `select profile_id, chat_id, kind, title, last_message_at, unread_count, muted, pinned, raw_json, updated_at from chats order by profile_id, chat_id`},
		{Entity: "chat_member", Query: `select profile_id, chat_id, contact_id, display_name, raw_json, updated_at from chat_members order by profile_id, chat_id, contact_id`},
		{Entity: "message", Query: `select profile_id, message_id, chat_id, sender_id, direction, message_type, sent_at, edited_at, deleted_at, text, normalized_text, source_db, source_rowid, raw_json, updated_at from messages order by profile_id, chat_id, coalesce(sent_at,''), message_id`},
		{Entity: "message_part", Query: `select profile_id, message_id, part_index, kind, text, media_id, url, raw_json from message_parts order by profile_id, message_id, part_index`},
		{Entity: "message_event", Query: `select event_id, profile_id, chat_id, message_id, event_type, event_at, payload_json from message_events order by profile_id, chat_id, event_at, event_id`},
		{Entity: "media", Query: `select profile_id, media_id, kind, source_path, archive_path, mime_type, byte_size, sha256, width, height, duration_ms, raw_json, updated_at from media_items order by profile_id, media_id`},
		{Entity: "favorite", Query: `select profile_id, favorite_id, kind, title, text, source_ref, raw_json, updated_at from favorites order by profile_id, favorite_id`},
		{Entity: "biz_account", Query: `select profile_id, account_id, display_name, raw_json, updated_at from biz_accounts order by profile_id, account_id`},
		{Entity: "article", Query: `select profile_id, article_id, account_id, title, url, summary, published_at, raw_json, updated_at from biz_articles order by profile_id, coalesce(published_at,''), article_id`},
		{Entity: "moment", Query: `select profile_id, moment_id, author_id, text, created_at, raw_json, updated_at from moments order by profile_id, coalesce(created_at,''), moment_id`},
	}
	switch scope {
	case "all":
		return all, nil
	case "messages":
		return filterExportQueries(all, "message", "message_part", "message_event"), nil
	case "articles":
		return filterExportQueries(all, "biz_account", "article"), nil
	case "favorites":
		return filterExportQueries(all, "favorite"), nil
	case "media":
		return filterExportQueries(all, "media"), nil
	case "moments":
		return filterExportQueries(all, "moment"), nil
	case "raw":
		return []exportQuery{{Entity: "raw_record", Query: `select id, profile_id, source_name, source_table, source_key, record_kind, payload_json, observed_at from raw_records order by profile_id, source_name, source_table, id`}}, nil
	default:
		return nil, fmt.Errorf("unsupported export scope %q", scope)
	}
}

func filterExportQueries(queries []exportQuery, entities ...string) []exportQuery {
	wanted := map[string]bool{}
	for _, entity := range entities {
		wanted[entity] = true
	}
	var out []exportQuery
	for _, query := range queries {
		if wanted[query.Entity] {
			out = append(out, query)
		}
	}
	return out
}

func (e env) runTUI(args []string) error {
	fs := newFlagSet("tui")
	if err := fs.Parse(args); err != nil {
		return err
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	result, err := arc.Query(e.ctx, `select message_id, chat_id, coalesce(sender_id,''), message_type, coalesce(sent_at,''), text from messages order by coalesce(sent_at,''), message_id limit 500`)
	if err != nil {
		return err
	}
	var rows []cktui.Row
	for _, value := range result.Values {
		id := fmt.Sprint(value["message_id"])
		rows = append(rows, cktui.Row{
			Source:    cktui.SourceLocal,
			Kind:      fmt.Sprint(value["message_type"]),
			ID:        id,
			ParentID:  fmt.Sprint(value["chat_id"]),
			Container: fmt.Sprint(value["chat_id"]),
			Author:    fmt.Sprint(value["sender_id"]),
			Title:     id,
			Text:      fmt.Sprint(value["text"]),
			CreatedAt: fmt.Sprint(value["sent_at"]),
		})
	}
	return cktui.Browse(e.ctx, cktui.BrowseOptions{AppName: "weicrawl", Title: "weicrawl archive", Rows: rows, JSON: e.format == output.JSON, Layout: cktui.LayoutChat, SourceKind: cktui.SourceLocal, SourceLocation: arc.Path(), Stdout: e.out})
}

func (e env) runCompletion(args []string) error {
	shell := "zsh"
	if len(args) > 0 {
		shell = args[0]
	}
	if shell != "zsh" {
		return output.UsageError{Err: fmt.Errorf("unsupported shell %q", shell)}
	}
	_, err := fmt.Fprintln(e.out, "#compdef weicrawl\n_arguments '1:command:(version init doctor metadata status sync unlock profiles contacts chats messages search favorites articles media runs sql export snapshot import tui completion)' '*::arg:->args'")
	return err
}

func (e env) write(label string, value any) error {
	if e.format == output.Text {
		switch v := value.(type) {
		case string:
			_, err := fmt.Fprintln(e.out, v)
			return err
		default:
			return output.Write(e.out, output.JSON, label, value)
		}
	}
	return output.Write(e.out, e.format, label, value)
}

func parseGlobals(args []string) (globals, []string, error) {
	var g globals
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			g.json = true
		case "--quiet":
			g.quiet = true
		case "--verbose":
			g.verbose = true
		case "--config":
			i++
			if i >= len(args) {
				return g, nil, output.UsageError{Err: errors.New("--config requires a value")}
			}
			g.configPath = args[i]
		case "--db":
			i++
			if i >= len(args) {
				return g, nil, output.UsageError{Err: errors.New("--db requires a value")}
			}
			g.dbPath = args[i]
		case "--profile":
			i++
			if i >= len(args) {
				return g, nil, output.UsageError{Err: errors.New("--profile requires a value")}
			}
			g.profile = args[i]
		default:
			if strings.HasPrefix(arg, "--config=") {
				g.configPath = strings.TrimPrefix(arg, "--config=")
			} else if strings.HasPrefix(arg, "--db=") {
				g.dbPath = strings.TrimPrefix(arg, "--db=")
			} else if strings.HasPrefix(arg, "--profile=") {
				g.profile = strings.TrimPrefix(arg, "--profile=")
			} else {
				rest = append(rest, arg)
			}
		}
	}
	return g, rest, nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func manifest() control.Manifest {
	m := control.NewManifest("weicrawl", "WeChat Crawl", "weicrawl")
	m.Description = "Local-first Weixin/WeChat archive"
	m.Branding.BundleIdentifier = "com.tencent.xinWeChat"
	m.Paths = control.Paths{
		DefaultConfig:   "~/.config/weicrawl/config.toml",
		ConfigEnv:       "WEICRAWL_CONFIG",
		DefaultDatabase: "~/.config/weicrawl/weicrawl.db",
		DefaultCache:    "~/.cache/weicrawl",
		DefaultLogs:     "~/.local/state/weicrawl/logs",
	}
	m.Privacy = control.Privacy{
		ContainsPrivateMessages: true,
		ExportsSecrets:          false,
		LocalOnlyScopes:         []string{"desktop-macos", "desktop-backup"},
	}
	m.Capabilities = []string{"local-sqlite", "desktop-snapshot", "fts-search", "snapshot-export", "tui"}
	for _, cmd := range []string{"doctor", "status", "sync", "search", "tui"} {
		m.Commands[cmd] = control.Command{Title: strings.Title(cmd), Argv: []string{"weicrawl", "--json", cmd}, JSON: true, Mutates: cmd == "sync"}
	}
	return m
}

func usage() string {
	return strings.TrimSpace(`weicrawl [global flags] <command> [args]

commands:
  init doctor metadata status sync unlock
  profiles contacts chats messages favorites articles media runs
  search sql export snapshot import tui completion version

global flags:
  --config <path>  config path
  --db <path>      archive db path
  --profile <id>   profile id or wxid
  --json           JSON output`)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func atoi(value any) int64 {
	n, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
	return n
}

var _ = atoi

var unsafeFilenameRE = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

func safeMarkdownName(value string) string {
	value = strings.Trim(unsafeFilenameRE.ReplaceAllString(value, "-"), ". ")
	if value == "" {
		return "chat"
	}
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}

func parseSince(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(value, "d"), 10, 64)
		if err != nil || days < 0 {
			return "", fmt.Errorf("invalid --since value %q", value)
		}
		return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339), nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return "", fmt.Errorf("invalid --since value %q", value)
	}
	return time.Now().UTC().Add(-duration).Format(time.RFC3339), nil
}
