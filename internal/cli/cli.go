package cli

import (
	"bufio"
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

type syncOptions struct {
	Source        string
	Profile       string
	IncludeMedia  bool
	MediaMode     string
	KeepSource    bool
	KeepDecrypted bool
	DecryptedDir  string
	BackupRoot    string
	ImportPath    string
	ImportFormat  string
	Since         string
	Concurrency   int
}

type syncAllResult struct {
	Source   string         `json:"source"`
	Status   string         `json:"status"`
	Results  []any          `json:"results,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Errors   []syncAllError `json:"errors,omitempty"`
}

type syncAllError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
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
	case "chat-members":
		return e.runList("chat_members", rest)
	case "messages":
		return e.runList("messages", rest)
	case "message-parts":
		return e.runList("message_parts", rest)
	case "message-events":
		return e.runList("message_events", rest)
	case "favorites":
		return e.runList("favorites", rest)
	case "biz-accounts":
		return e.runList("biz_accounts", rest)
	case "articles":
		return e.runList("biz_articles", rest)
	case "media":
		return e.runList("media_items", rest)
	case "moments":
		return e.runList("moments", rest)
	case "raw-records":
		return e.runList("raw_records", rest)
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
	keysPath := fs.String("keys", "", "wechat_keys.json path for unlock probe")
	snapshotPath := fs.String("snapshot", "", "copied snapshot profile root for unlock probe")
	sqlcipherPath := fs.String("sqlcipher", "", "sqlcipher binary path for unlock probe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	arc, dbErr := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	var schemaVersion int
	ftsOK := false
	var ftsErr error
	if dbErr == nil {
		defer arc.Close()
		schemaVersion, _ = arc.SchemaVersion(e.ctx)
		ftsOK, ftsErr = checkFTSHealth(e.ctx, arc)
	}
	disc := desktopmac.Discover(e.ctx, e.loaded.Config.DesktopMacOS.ContainerPath)
	resolvedSQLCipher, sqlcipherErr := unlock.FindSQLCipher(config.Expand(*sqlcipherPath))
	metadataOK, metadataErr := checkControlMetadata()
	checks := []map[string]any{
		{"id": "config_readable", "ok": true, "path": e.loaded.Path},
		{"id": "archive_db_openable", "ok": dbErr == nil, "path": e.loaded.Config.Archive.DBPath, "error": errString(dbErr)},
		{"id": "schema_version", "ok": schemaVersion == schema.Version, "current": schemaVersion, "want": schema.Version},
		{"id": "fts_health", "ok": dbErr == nil && ftsOK, "error": errString(ftsErr)},
		{"id": "crawlkit_metadata_valid", "ok": metadataOK, "error": errString(metadataErr)},
		{"id": "wechat_app_present", "ok": disc.AppPresent, "path": disc.AppPath, "version": disc.AppVersion},
		{"id": "wechat_running", "ok": true, "running": disc.Running},
		{"id": "wechat_container_present", "ok": disc.ContainerPresent, "path": disc.ContainerPath},
		{"id": "profile_discovery", "ok": len(disc.ProfileRoots) > 0, "profiles": len(disc.ProfileRoots)},
		{"id": "database_shards", "ok": disc.DatabaseCount > 0, "count": disc.DatabaseCount},
		{"id": "source_db_encryption_probe", "ok": true, "encrypted_count": disc.EncryptedDBCount, "database_count": disc.DatabaseCount},
		{"id": "backup_discovery", "ok": true, "backup_dirs": len(disc.BackupDirs)},
		{"id": "unlock_configured", "ok": e.loaded.Config.Unlock.AllowProcessInspect || e.loaded.Config.Unlock.AllowKeychain || e.loaded.Config.Unlock.StoreKeychain},
		{"id": "unlock_key_manifest_supported", "ok": true, "method": "unlock desktop --keys <wechat_keys.json> --snapshot <copied-profile-root> --out <decrypted-dir>"},
		{"id": "sqlcipher_available", "ok": sqlcipherErr == nil, "path": resolvedSQLCipher, "error": errString(sqlcipherErr)},
		{"id": "decrypted_snapshot_retention", "ok": !e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots, "enabled": e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots},
		{"id": "probe_unlock_requested", "ok": !*probeUnlock || sqlcipherErr == nil, "skipped": !*probeUnlock, "note": "probe requires sqlcipher and an external key manifest"},
	}
	if *probeUnlock && (strings.TrimSpace(*keysPath) == "" || strings.TrimSpace(*snapshotPath) == "") {
		missing := []string{}
		if strings.TrimSpace(*keysPath) == "" {
			missing = append(missing, "--keys")
		}
		if strings.TrimSpace(*snapshotPath) == "" {
			missing = append(missing, "--snapshot")
		}
		checks = append(checks, map[string]any{
			"id":      "unlock_readiness",
			"ok":      false,
			"skipped": true,
			"missing": missing,
			"note":    "pass --keys and --snapshot to probe unlock readiness without decrypting",
		})
	}
	if *probeUnlock && strings.TrimSpace(*keysPath) != "" && strings.TrimSpace(*snapshotPath) != "" {
		check, err := unlock.CheckSnapshotKeys(unlock.DecryptOptions{
			SnapshotDir:   config.Expand(*snapshotPath),
			KeysPath:      config.Expand(*keysPath),
			SQLCipherPath: config.Expand(*sqlcipherPath),
		})
		checks = append(checks, map[string]any{
			"id":    "unlock_readiness",
			"ok":    err == nil && check.Ready,
			"check": check,
			"error": errString(err),
		})
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
		control.NewCount("biz_accounts", "Public accounts", status.BizAccountCount),
		control.NewCount("biz_articles", "Public-account articles", status.PublicAccountArticleCount),
		control.NewCount("moments", "Moments", status.MomentCount),
	}
	if status.LastSyncRun != nil {
		ckStatus.LastSyncAt = status.LastSyncRun.FinishedAt
	}
	disc := desktopmac.Discover(e.ctx, e.loaded.Config.DesktopMacOS.ContainerPath)
	return e.write("status", map[string]any{
		"control": ckStatus,
		"archive": status,
		"source": map[string]any{
			"desktop_macos": map[string]any{
				"app_path":                 disc.AppPath,
				"app_version":              disc.AppVersion,
				"bundle_id":                disc.BundleID,
				"bundle_version":           disc.BundleVersion,
				"running":                  disc.Running,
				"container_path":           e.loaded.Config.DesktopMacOS.ContainerPath,
				"container_present":        disc.ContainerPresent,
				"profile_count":            len(disc.ProfileRoots),
				"database_count":           disc.DatabaseCount,
				"encrypted_database_count": disc.EncryptedDBCount,
				"keep_source_snapshots":    e.loaded.Config.DesktopMacOS.KeepSourceSnapshots,
				"keep_decrypted_snapshots": e.loaded.Config.DesktopMacOS.KeepDecryptedSnapshots,
			},
		},
	})
}

func checkFTSHealth(ctx context.Context, arc *archive.Archive) (bool, error) {
	for _, table := range []string{"message_fts", "article_fts"} {
		var exists int
		if err := arc.DB().QueryRowContext(ctx, `select count(*) from sqlite_master where type = 'table' and name = ?`, table).Scan(&exists); err != nil {
			return false, err
		}
		if exists != 1 {
			return false, fmt.Errorf("%s missing", table)
		}
		var count int64
		if err := arc.DB().QueryRowContext(ctx, `select count(*) from `+quoteSQLIdent(table)).Scan(&count); err != nil {
			return false, err
		}
	}
	return true, nil
}

func checkControlMetadata() (bool, error) {
	meta := manifest()
	if strings.TrimSpace(meta.ID) != "weicrawl" {
		return false, fmt.Errorf("manifest id = %q", meta.ID)
	}
	if strings.TrimSpace(meta.Binary.Name) != "weicrawl" {
		return false, fmt.Errorf("manifest binary = %q", meta.Binary.Name)
	}
	for _, command := range []string{"doctor", "status", "sync", "search", "tui"} {
		if _, ok := meta.Commands[command]; !ok {
			return false, fmt.Errorf("manifest missing command %q", command)
		}
	}
	return true, nil
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
	importPath := fs.String("import-path", "", "artifact path for source=import")
	importFormat := fs.String("format", "jsonl", "import artifact format")
	full := fs.Bool("full", false, "full sync")
	since := fs.String("since", "", "lower bound timestamp")
	concurrency := fs.Int("concurrency", 1, "copy concurrency")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sinceValue, err := parseSince(*since)
	if err != nil {
		return output.UsageError{Err: err}
	}
	if *full && sinceValue != "" {
		return output.UsageError{Err: errors.New("use either --full or --since, not both")}
	}
	if *concurrency < 1 {
		return output.UsageError{Err: errors.New("--concurrency must be at least 1")}
	}
	if *keepDecrypted && strings.TrimSpace(*decryptedDir) == "" {
		return output.UsageError{Err: errors.New("--keep-decrypted-snapshot requires --decrypted-dir")}
	}
	if *mediaMode != "" && *mediaMode != "metadata" && *mediaMode != "copy" {
		return output.UsageError{Err: fmt.Errorf("unsupported media mode %q", *mediaMode)}
	}
	if *source != "desktop-macos" && *source != "desktop-backup" && *source != "all" && *source != "official-account-api" && *source != "import" {
		return output.UsageError{Err: fmt.Errorf("source %q is not implemented yet", *source)}
	}
	if *source == "all" && strings.TrimSpace(*importPath) != "" && *importFormat != "jsonl" {
		return output.UsageError{Err: fmt.Errorf("unsupported import format %q", *importFormat)}
	}
	if sinceValue != "" && (*source == "official-account-api" || (*source == "all" && e.loaded.Config.OfficialAccount.Enabled)) {
		return output.UsageError{Err: fmt.Errorf("--since is not supported with source %q yet", *source)}
	}
	opts := syncOptions{
		Source:        *source,
		Profile:       *profileFlag,
		IncludeMedia:  *includeMedia,
		MediaMode:     *mediaMode,
		KeepSource:    *keepSource,
		KeepDecrypted: *keepDecrypted,
		DecryptedDir:  *decryptedDir,
		BackupRoot:    *backupRoot,
		ImportPath:    *importPath,
		ImportFormat:  *importFormat,
		Since:         sinceValue,
		Concurrency:   *concurrency,
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	if opts.Source == "all" {
		return e.runSyncAll(arc, opts)
	}
	if opts.Source == "official-account-api" {
		result, err := e.syncOfficial(arc)
		if err != nil {
			return err
		}
		return e.write("sync", result)
	}
	if opts.Source == "desktop-backup" {
		if strings.TrimSpace(opts.BackupRoot) == "" {
			return output.UsageError{Err: errors.New("--backup-root is required for desktop-backup")}
		}
		result, err := e.syncBackup(arc, opts)
		if err != nil {
			return err
		}
		return e.write("sync", result)
	}
	if opts.Source == "import" {
		if strings.TrimSpace(opts.ImportPath) == "" {
			return output.UsageError{Err: errors.New("--import-path is required for source=import")}
		}
		if opts.ImportFormat != "jsonl" {
			return output.UsageError{Err: fmt.Errorf("unsupported import format %q", opts.ImportFormat)}
		}
		result, err := e.importJSONLResult(arc, config.Expand(opts.ImportPath), opts.Since)
		if err != nil {
			return err
		}
		return e.write("import", result)
	}
	result, err := e.syncDesktop(arc, opts)
	if err != nil {
		return err
	}
	return e.write("sync", result)
}

func (e env) runSyncAll(arc *archive.Archive, opts syncOptions) error {
	result := syncAllResult{Source: "all", Status: "success"}
	run := func(source string, fn func() (any, error)) {
		item, err := fn()
		if err != nil {
			result.Errors = append(result.Errors, syncAllError{Source: source, Error: err.Error()})
			return
		}
		result.Results = append(result.Results, item)
	}
	if e.loaded.Config.DesktopMacOS.Enabled {
		run("desktop-macos", func() (any, error) {
			return e.syncDesktop(arc, opts)
		})
	}
	if e.loaded.Config.OfficialAccount.Enabled {
		run("official-account-api", func() (any, error) {
			return e.syncOfficial(arc)
		})
	}
	if strings.TrimSpace(opts.BackupRoot) != "" {
		run("desktop-backup", func() (any, error) {
			return e.syncBackup(arc, opts)
		})
	}
	if strings.TrimSpace(opts.ImportPath) != "" {
		run("import", func() (any, error) {
			if opts.ImportFormat != "jsonl" {
				return nil, output.UsageError{Err: fmt.Errorf("unsupported import format %q", opts.ImportFormat)}
			}
			return e.importJSONLResult(arc, config.Expand(opts.ImportPath), opts.Since)
		})
	}
	result.Status = aggregateSyncStatus(result.Results, len(result.Errors))
	if len(result.Results) == 0 {
		if len(result.Errors) > 0 {
			result.Status = "failed"
			if err := e.write("sync", result); err != nil {
				return err
			}
			return errors.New("all selected sync sources failed")
		}
		result.Status = "skipped"
		result.Warnings = append(result.Warnings, "no configured or explicitly selected sources ran")
	}
	return e.write("sync", result)
}

func aggregateSyncStatus(results []any, errorCount int) string {
	if errorCount > 0 {
		if len(results) == 0 {
			return "failed"
		}
		return "partial"
	}
	if len(results) == 0 {
		return "skipped"
	}
	successes := 0
	skipped := 0
	for _, item := range results {
		switch syncStatus(item) {
		case "partial":
			return "partial"
		case "failed":
			return "partial"
		case "skipped":
			skipped++
		default:
			successes++
		}
	}
	if successes == 0 && skipped > 0 {
		return "skipped"
	}
	if skipped > 0 {
		return "partial"
	}
	return "success"
}

func syncStatus(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(bytes, &payload); err != nil {
		return ""
	}
	return payload.Status
}

func (e env) syncOfficial(arc *archive.Archive) (officialaccount.Result, error) {
	return officialaccount.Sync(e.ctx, arc, officialaccount.Options{Config: e.loaded.Config.OfficialAccount})
}

func (e env) syncBackup(arc *archive.Archive, opts syncOptions) (backup.Result, error) {
	return backup.Sync(e.ctx, arc, backup.Options{Root: config.Expand(opts.BackupRoot), ProfileID: opts.Profile, Since: opts.Since})
}

func (e env) syncDesktop(arc *archive.Archive, opts syncOptions) (desktopmac.SyncResult, error) {
	if strings.TrimSpace(opts.DecryptedDir) != "" {
		profileID := opts.Profile
		if profileID == "" {
			profileID = "decrypted"
		}
		return desktopmac.SyncDecryptedDirectory(e.ctx, arc, profileID, config.Expand(opts.DecryptedDir), "", opts.Since, opts.KeepDecrypted)
	}
	disc := desktopmac.Discover(e.ctx, e.loaded.Config.DesktopMacOS.ContainerPath)
	profile, ok := desktopmac.SelectProfile(disc, opts.Profile)
	if !ok {
		return desktopmac.SyncResult{}, fmt.Errorf("profile %q not found; discovered %d profiles", opts.Profile, len(disc.ProfileRoots))
	}
	return desktopmac.SyncDesktopSnapshot(e.ctx, arc, desktopmac.SnapshotOptions{
		CacheDir:     e.loaded.Config.Archive.CacheDir,
		Profile:      profile,
		AppVersion:   disc.AppVersion,
		IncludeMedia: opts.IncludeMedia,
		MediaMode:    opts.MediaMode,
		Keep:         opts.KeepSource,
		Since:        opts.Since,
		Concurrency:  opts.Concurrency,
	})
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
	syncAfterUnlock := fs.Bool("sync", false, "ingest decrypted output after unlock")
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
		if *explain {
			if strings.TrimSpace(*snapshotPath) == "" {
				return output.UsageError{Err: errors.New("unlock desktop --explain with --keys requires --snapshot")}
			}
			check, err := unlock.CheckSnapshotKeys(unlock.DecryptOptions{
				SnapshotDir:   config.Expand(*snapshotPath),
				OutputDir:     config.Expand(*outDir),
				KeysPath:      config.Expand(*keysPath),
				SQLCipherPath: config.Expand(*sqlcipherPath),
			})
			if err != nil {
				return err
			}
			return e.write("unlock", map[string]any{
				"subcommand":  sub,
				"method":      "key-manifest+sqlcipher",
				"app_version": disc.AppVersion,
				"profile":     *profile,
				"persisted":   false,
				"dry_run":     true,
				"available":   check.Ready,
				"check":       check,
				"next":        "rerun without --explain to decrypt the copied snapshot",
			})
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
		nextProfile := strings.TrimSpace(*profile)
		if nextProfile == "" {
			nextProfile = "decrypted"
		}
		payload := map[string]any{
			"subcommand":  sub,
			"method":      "key-manifest+sqlcipher",
			"app_version": disc.AppVersion,
			"profile":     *profile,
			"persisted":   false,
			"decrypt":     result,
			"next":        fmt.Sprintf("run `weicrawl sync --source desktop-macos --profile %s --decrypted-dir %s`", nextProfile, result.OutputDir),
		}
		if *syncAfterUnlock {
			arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
			if err != nil {
				return err
			}
			defer arc.Close()
			syncResult, err := desktopmac.SyncDecryptedDirectory(e.ctx, arc, nextProfile, result.OutputDir, disc.AppVersion, "", true)
			if err != nil {
				return err
			}
			payload["sync"] = syncResult
			payload["next"] = "run `weicrawl status --json` or `weicrawl search --json <query>`"
		}
		return e.write("unlock", payload)
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
			outputText, _ := redactKeyScanOutput(out)
			return fmt.Errorf("key scan failed: %w\n%s", err, outputText)
		}
		outputText, redacted := redactKeyScanOutput(out)
		written, err := unlock.WriteDefaultKeyManifestFromScan(out, plan.OutputPath)
		if err != nil {
			return fmt.Errorf("key scan manifest failed: %w\n%s", err, outputText)
		}
		return e.write("unlock", map[string]any{
			"command":          plan.Command,
			"manifest_path":    plan.OutputPath,
			"manifest_written": written,
			"output_redacted":  outputText,
			"output_bytes":     len(out),
			"redacted":         redacted,
		})
	case "forget":
		payload["forgotten"] = false
		payload["available"] = false
		payload["warning"] = "no persisted unlock material is managed by weicrawl yet"
		payload["next"] = "nothing to forget; decrypted snapshots are caller-managed"
	default:
		return output.UsageError{Err: fmt.Errorf("unknown unlock subcommand %q", sub)}
	}
	return e.write("unlock", payload)
}

var keyScanSensitiveRE = regexp.MustCompile(`(?i)(?:0x|x')?[0-9a-f]{64}'?`)

func redactKeyScanOutput(output []byte) (string, bool) {
	text := strings.TrimSpace(string(output))
	redacted := keyScanSensitiveRE.MatchString(text)
	text = keyScanSensitiveRE.ReplaceAllString(text, "[redacted-key]")
	const maxOutput = 4096
	if len(text) > maxOutput {
		text = text[:maxOutput] + "...[truncated]"
	}
	return text, redacted
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
	if !isReadOnlySQL(query) {
		return output.UsageError{Err: errors.New("sql is read-only; use select, with, or read-only pragma statements")}
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

func isReadOnlySQL(query string) bool {
	q := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if q == "" {
		return false
	}
	for _, token := range []string{"insert", "update", "delete", "replace", "drop", "alter", "create", "vacuum", "attach", "detach", "reindex"} {
		if containsSQLToken(q, token) {
			return false
		}
	}
	return strings.HasPrefix(q, "select ") || q == "select" || strings.HasPrefix(q, "with ") || isReadOnlyPragma(q)
}

func containsSQLToken(query, token string) bool {
	for _, part := range strings.FieldsFunc(query, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		if part == token {
			return true
		}
	}
	return false
}

func isReadOnlyPragma(query string) bool {
	if !strings.HasPrefix(query, "pragma ") {
		return false
	}
	name := strings.TrimSpace(strings.TrimPrefix(query, "pragma "))
	if i := strings.IndexAny(name, "( ="); i >= 0 {
		name = name[:i]
	}
	switch name {
	case "table_info", "table_xinfo", "index_list", "index_info", "index_xinfo", "database_list", "foreign_key_list", "quick_check", "integrity_check", "compile_options":
		return true
	default:
		return false
	}
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
	format := fs.String("format", "snapshot", "snapshot or jsonl")
	since := fs.String("since", "", "lower bound timestamp for jsonl imports")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return output.UsageError{Err: errors.New("import path is required")}
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	if *format == "jsonl" {
		sinceValue, err := parseSince(*since)
		if err != nil {
			return output.UsageError{Err: err}
		}
		return e.importJSONL(arc, config.Expand(fs.Arg(0)), sinceValue)
	}
	if strings.TrimSpace(*since) != "" {
		return output.UsageError{Err: errors.New("--since is only supported for jsonl imports")}
	}
	if *format != "snapshot" {
		return output.UsageError{Err: fmt.Errorf("unsupported import format %q", *format)}
	}
	manifest, err := snapshot.Import(e.ctx, snapshot.ImportOptions{DB: arc.DB(), RootDir: config.Expand(fs.Arg(0)), DeleteTables: schema.SnapshotTables})
	if err != nil {
		return err
	}
	if err := arc.RebuildFTS(e.ctx); err != nil {
		return err
	}
	return e.write("import", manifest)
}

type jsonlImportResult struct {
	Source  string         `json:"source"`
	Path    string         `json:"path"`
	Since   string         `json:"since,omitempty"`
	Rows    int            `json:"rows"`
	Skipped int            `json:"skipped,omitempty"`
	Counts  map[string]int `json:"counts"`
}

type importEntity struct {
	Table   string
	Columns []string
}

func (e env) importJSONL(arc *archive.Archive, path, since string) error {
	result, err := e.importJSONLResult(arc, path, since)
	if err != nil {
		return err
	}
	return e.write("import", result)
}

func (e env) importJSONLResult(arc *archive.Archive, path, since string) (jsonlImportResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return jsonlImportResult{}, err
	}
	defer file.Close()
	entities := importEntities()
	result := jsonlImportResult{Source: "import-jsonl", Path: path, Since: since, Counts: map[string]int{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	line := 0
	var rows []map[string]any
	for scanner.Scan() {
		line++
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return result, fmt.Errorf("decode jsonl line %d: %w", line, err)
		}
		entityName := strings.TrimSpace(fmt.Sprint(row["entity"]))
		if _, ok := entities[entityName]; !ok {
			return result, fmt.Errorf("unsupported jsonl entity %q on line %d", entityName, line)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	keptMessages := jsonlKeptMessages(rows, since)
	for i, row := range rows {
		entityName := strings.TrimSpace(fmt.Sprint(row["entity"]))
		entity := entities[entityName]
		if !shouldImportJSONLRow(row, since, keptMessages) {
			result.Skipped++
			continue
		}
		if err := insertJSONLEntity(e.ctx, arc, entity, row); err != nil {
			return result, fmt.Errorf("import %s line %d: %w", entityName, i+1, err)
		}
		result.Rows++
		result.Counts[entityName]++
	}
	if err := arc.RebuildFTS(e.ctx); err != nil {
		return result, err
	}
	started := time.Now().UTC()
	if err := arc.InsertSyncRun(e.ctx, archive.SyncRun{
		RunID:               "import-" + started.Format("20060102T150405.000000000Z"),
		Source:              result.Source,
		StartedAt:           started.Format(time.RFC3339),
		FinishedAt:          time.Now().UTC().Format(time.RFC3339),
		Status:              "success",
		SourceRoot:          path,
		ImportedProfiles:    int64(result.Counts["profile"]),
		ImportedContacts:    int64(result.Counts["contact"]),
		ImportedChats:       int64(result.Counts["chat"]),
		ImportedMessages:    int64(result.Counts["message"]),
		ImportedParts:       int64(result.Counts["message_part"]),
		ImportedEvents:      int64(result.Counts["message_event"]),
		ImportedMedia:       int64(result.Counts["media"]),
		ImportedBizAccounts: int64(result.Counts["biz_account"]),
		ImportedArticles:    int64(result.Counts["article"]),
		ImportedFavorites:   int64(result.Counts["favorite"]),
		ImportedMoments:     int64(result.Counts["moment"]),
		ImportedRawRecords:  int64(result.Counts["raw_record"]),
	}); err != nil {
		return result, err
	}
	return result, nil
}

func jsonlKeptMessages(rows []map[string]any, since string) map[string]bool {
	kept := map[string]bool{}
	if strings.TrimSpace(since) == "" {
		return kept
	}
	for _, row := range rows {
		if strings.TrimSpace(fmt.Sprint(row["entity"])) != "message" {
			continue
		}
		if timestampAtOrAfter(fmt.Sprint(row["sent_at"]), since) {
			kept[jsonlMessageKey(row)] = true
		}
	}
	return kept
}

func shouldImportJSONLRow(row map[string]any, since string, keptMessages map[string]bool) bool {
	since = strings.TrimSpace(since)
	if since == "" {
		return true
	}
	entityName := strings.TrimSpace(fmt.Sprint(row["entity"]))
	switch entityName {
	case "message":
		return timestampAtOrAfter(fmt.Sprint(row["sent_at"]), since)
	case "message_part":
		return keptMessages[jsonlMessageKey(row)]
	case "message_event":
		if !timestampAtOrAfter(fmt.Sprint(row["event_at"]), since) {
			return false
		}
		key := jsonlMessageKey(row)
		return key == "\x00" || keptMessages[key]
	case "favorite":
		sourceRef := strings.TrimSpace(fmt.Sprint(row["source_ref"]))
		if sourceRef == "" {
			return true
		}
		key := jsonlString(row, "profile_id") + "\x00" + sourceRef
		return keptMessages[key]
	case "article":
		return timestampAtOrAfter(fmt.Sprint(row["published_at"]), since)
	case "moment":
		return timestampAtOrAfter(fmt.Sprint(row["created_at"]), since)
	case "raw_record":
		return timestampAtOrAfter(fmt.Sprint(row["observed_at"]), since)
	default:
		return true
	}
}

func jsonlMessageKey(row map[string]any) string {
	return jsonlString(row, "profile_id") + "\x00" + jsonlString(row, "message_id")
}

func jsonlString(row map[string]any, key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func timestampAtOrAfter(value, since string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	valueTime, valueErr := time.Parse(time.RFC3339, value)
	sinceTime, sinceErr := time.Parse(time.RFC3339, since)
	if valueErr == nil && sinceErr == nil {
		return !valueTime.Before(sinceTime)
	}
	return value >= since
}

func importEntities() map[string]importEntity {
	return map[string]importEntity{
		"profile": {
			Table:   "profiles",
			Columns: []string{"profile_id", "wxid", "display_name", "source_root", "app_version", "raw_json", "updated_at"},
		},
		"contact": {
			Table:   "contacts",
			Columns: []string{"profile_id", "contact_id", "alias", "display_name", "remark_name", "kind", "avatar_ref", "raw_json", "updated_at"},
		},
		"chat": {
			Table:   "chats",
			Columns: []string{"profile_id", "chat_id", "kind", "title", "last_message_at", "unread_count", "muted", "pinned", "raw_json", "updated_at"},
		},
		"chat_member": {
			Table:   "chat_members",
			Columns: []string{"profile_id", "chat_id", "contact_id", "display_name", "raw_json", "updated_at"},
		},
		"message": {
			Table:   "messages",
			Columns: []string{"profile_id", "message_id", "chat_id", "sender_id", "direction", "message_type", "sent_at", "edited_at", "deleted_at", "text", "normalized_text", "source_db", "source_rowid", "raw_json", "updated_at"},
		},
		"message_part": {
			Table:   "message_parts",
			Columns: []string{"profile_id", "message_id", "part_index", "kind", "text", "media_id", "url", "raw_json"},
		},
		"message_event": {
			Table:   "message_events",
			Columns: []string{"event_id", "profile_id", "chat_id", "message_id", "event_type", "event_at", "payload_json"},
		},
		"media": {
			Table:   "media_items",
			Columns: []string{"profile_id", "media_id", "kind", "source_path", "archive_path", "mime_type", "byte_size", "sha256", "width", "height", "duration_ms", "raw_json", "updated_at"},
		},
		"favorite": {
			Table:   "favorites",
			Columns: []string{"profile_id", "favorite_id", "kind", "title", "text", "source_ref", "raw_json", "updated_at"},
		},
		"biz_account": {
			Table:   "biz_accounts",
			Columns: []string{"profile_id", "account_id", "display_name", "raw_json", "updated_at"},
		},
		"article": {
			Table:   "biz_articles",
			Columns: []string{"profile_id", "article_id", "account_id", "title", "url", "summary", "published_at", "raw_json", "updated_at"},
		},
		"moment": {
			Table:   "moments",
			Columns: []string{"profile_id", "moment_id", "author_id", "text", "created_at", "raw_json", "updated_at"},
		},
		"raw_record": {
			Table:   "raw_records",
			Columns: []string{"id", "profile_id", "source_name", "source_table", "source_key", "record_kind", "payload_json", "observed_at"},
		},
	}
}

func insertJSONLEntity(ctx context.Context, arc *archive.Archive, entity importEntity, row map[string]any) error {
	columns := make([]string, 0, len(entity.Columns))
	placeholders := make([]string, 0, len(entity.Columns))
	values := make([]any, 0, len(entity.Columns))
	for _, column := range entity.Columns {
		value, ok := row[column]
		if !ok {
			continue
		}
		columns = append(columns, quoteSQLIdent(column))
		placeholders = append(placeholders, "?")
		values = append(values, normalizeJSONLValue(value))
	}
	if len(columns) == 0 {
		return errors.New("no importable columns")
	}
	query := "insert or replace into " + quoteSQLIdent(entity.Table) + "(" + strings.Join(columns, ",") + ") values(" + strings.Join(placeholders, ",") + ")"
	_, err := arc.DB().ExecContext(ctx, query, values...)
	return err
}

func normalizeJSONLValue(value any) any {
	switch v := value.(type) {
	case map[string]any, []any:
		bytes, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(bytes)
	default:
		return v
	}
}

func quoteSQLIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
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
	scope := fs.String("scope", "all", "messages, articles, favorites, media, moments, or all")
	layoutFlag := fs.String("layout", "auto", "auto, chat, document, or list")
	limit := fs.Int("limit", 500, "row limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	arc, err := archive.Open(e.ctx, e.loaded.Config.Archive.DBPath)
	if err != nil {
		return err
	}
	defer arc.Close()
	rows, err := e.tuiRows(arc, *scope, *limit)
	if err != nil {
		return err
	}
	layout, err := parseTUILayout(*layoutFlag)
	if err != nil {
		return output.UsageError{Err: err}
	}
	return cktui.Browse(e.ctx, cktui.BrowseOptions{AppName: "weicrawl", Title: "weicrawl archive", Rows: rows, JSON: e.format == output.JSON, Layout: layout, SourceKind: cktui.SourceLocal, SourceLocation: arc.Path(), Stdout: e.out})
}

func (e env) tuiRows(arc *archive.Archive, scope string, limit int) ([]cktui.Row, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "all"
	}
	var rows []cktui.Row
	add := func(more []cktui.Row) {
		rows = append(rows, more...)
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	switch scope {
	case "all":
		messageRows, err := e.tuiMessageRows(arc, limit)
		if err != nil {
			return nil, err
		}
		add(messageRows)
		for _, loader := range []func(*archive.Archive, int) ([]cktui.Row, error){
			e.tuiArticleRows,
			e.tuiFavoriteRows,
			e.tuiMediaRows,
			e.tuiMomentRows,
		} {
			if len(rows) >= limit {
				break
			}
			more, err := loader(arc, limit-len(rows))
			if err != nil {
				return nil, err
			}
			add(more)
		}
	case "messages":
		return e.tuiMessageRows(arc, limit)
	case "articles":
		return e.tuiArticleRows(arc, limit)
	case "favorites":
		return e.tuiFavoriteRows(arc, limit)
	case "media":
		return e.tuiMediaRows(arc, limit)
	case "moments":
		return e.tuiMomentRows(arc, limit)
	default:
		return nil, output.UsageError{Err: fmt.Errorf("unsupported tui scope %q", scope)}
	}
	if rows == nil {
		rows = []cktui.Row{}
	}
	return rows, nil
}

func (e env) tuiMessageRows(arc *archive.Archive, limit int) ([]cktui.Row, error) {
	result, err := arc.Query(e.ctx, `select message_id, chat_id, coalesce(sender_id,''), message_type, coalesce(sent_at,''), text from messages order by coalesce(sent_at,''), message_id limit ?`, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]cktui.Row, 0, len(result.Values))
	for _, value := range result.Values {
		id := fmt.Sprint(value["message_id"])
		rows = append(rows, cktui.Row{
			Source:    cktui.SourceLocal,
			Kind:      "message:" + fmt.Sprint(value["message_type"]),
			ID:        id,
			ParentID:  fmt.Sprint(value["chat_id"]),
			Container: fmt.Sprint(value["chat_id"]),
			Author:    fmt.Sprint(value["sender_id"]),
			Title:     id,
			Text:      fmt.Sprint(value["text"]),
			CreatedAt: fmt.Sprint(value["sent_at"]),
		})
	}
	return rows, nil
}

func (e env) tuiArticleRows(arc *archive.Archive, limit int) ([]cktui.Row, error) {
	result, err := arc.Query(e.ctx, `select article_id, coalesce(account_id,''), title, coalesce(summary,''), coalesce(url,''), coalesce(published_at,'') from biz_articles order by coalesce(published_at,''), article_id limit ?`, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]cktui.Row, 0, len(result.Values))
	for _, value := range result.Values {
		rows = append(rows, cktui.Row{
			Source:    cktui.SourceLocal,
			Kind:      "article",
			ID:        fmt.Sprint(value["article_id"]),
			ParentID:  fmt.Sprint(value["account_id"]),
			Container: firstDisplay(value["account_id"], "official-account"),
			Title:     firstDisplay(value["title"], value["article_id"]),
			Text:      fmt.Sprint(value["summary"]),
			URL:       fmt.Sprint(value["url"]),
			CreatedAt: fmt.Sprint(value["published_at"]),
		})
	}
	return rows, nil
}

func (e env) tuiFavoriteRows(arc *archive.Archive, limit int) ([]cktui.Row, error) {
	result, err := arc.Query(e.ctx, `select favorite_id, kind, coalesce(title,''), text, coalesce(source_ref,''), coalesce(updated_at,'') from favorites order by updated_at, favorite_id limit ?`, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]cktui.Row, 0, len(result.Values))
	for _, value := range result.Values {
		rows = append(rows, cktui.Row{
			Source:    cktui.SourceLocal,
			Kind:      "favorite:" + fmt.Sprint(value["kind"]),
			ID:        fmt.Sprint(value["favorite_id"]),
			ParentID:  fmt.Sprint(value["source_ref"]),
			Container: "favorites",
			Title:     firstDisplay(value["title"], value["favorite_id"]),
			Text:      fmt.Sprint(value["text"]),
			UpdatedAt: fmt.Sprint(value["updated_at"]),
		})
	}
	return rows, nil
}

func (e env) tuiMediaRows(arc *archive.Archive, limit int) ([]cktui.Row, error) {
	result, err := arc.Query(e.ctx, `select media_id, kind, coalesce(source_path,''), coalesce(archive_path,''), coalesce(mime_type,''), coalesce(byte_size,0), coalesce(updated_at,'') from media_items order by updated_at, media_id limit ?`, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]cktui.Row, 0, len(result.Values))
	for _, value := range result.Values {
		path := firstDisplay(value["archive_path"], value["source_path"])
		rows = append(rows, cktui.Row{
			Source:    cktui.SourceLocal,
			Kind:      "media:" + fmt.Sprint(value["kind"]),
			ID:        fmt.Sprint(value["media_id"]),
			Container: "media",
			Title:     firstDisplay(filepath.Base(path), value["media_id"]),
			Text:      path,
			UpdatedAt: fmt.Sprint(value["updated_at"]),
			Fields: map[string]string{
				"mime_type": fmt.Sprint(value["mime_type"]),
				"byte_size": fmt.Sprint(value["byte_size"]),
			},
		})
	}
	return rows, nil
}

func (e env) tuiMomentRows(arc *archive.Archive, limit int) ([]cktui.Row, error) {
	result, err := arc.Query(e.ctx, `select moment_id, coalesce(author_id,''), text, coalesce(created_at,'') from moments order by created_at, moment_id limit ?`, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]cktui.Row, 0, len(result.Values))
	for _, value := range result.Values {
		rows = append(rows, cktui.Row{
			Source:    cktui.SourceLocal,
			Kind:      "moment",
			ID:        fmt.Sprint(value["moment_id"]),
			Container: "moments",
			Author:    fmt.Sprint(value["author_id"]),
			Title:     firstDisplay(value["moment_id"], "moment"),
			Text:      fmt.Sprint(value["text"]),
			CreatedAt: fmt.Sprint(value["created_at"]),
		})
	}
	return rows, nil
}

func parseTUILayout(value string) (cktui.LayoutPreset, error) {
	switch strings.TrimSpace(value) {
	case "", "auto":
		return cktui.LayoutAuto, nil
	case "chat":
		return cktui.LayoutChat, nil
	case "document":
		return cktui.LayoutDocument, nil
	case "list":
		return cktui.LayoutList, nil
	default:
		return cktui.LayoutAuto, fmt.Errorf("unsupported tui layout %q", value)
	}
}

func firstDisplay(values ...any) string {
	for _, value := range values {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func (e env) runCompletion(args []string) error {
	shell := "zsh"
	if len(args) > 0 {
		shell = args[0]
	}
	if shell != "zsh" {
		return output.UsageError{Err: fmt.Errorf("unsupported shell %q", shell)}
	}
	_, err := fmt.Fprintln(e.out, "#compdef weicrawl\n_arguments '1:command:(version init doctor metadata status sync unlock profiles contacts chats chat-members messages message-parts message-events search favorites biz-accounts articles media moments raw-records runs sql export snapshot import tui completion)' '*::arg:->args'")
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
		LocalOnlyScopes:         []string{"desktop-macos", "desktop-backup", "decrypted-dir", "import"},
	}
	m.Capabilities = []string{
		"local-sqlite",
		"desktop-snapshot",
		"desktop-backup",
		"decrypted-db-import",
		"unlock-sync",
		"fts-search",
		"jsonl-import",
		"jsonl-export",
		"markdown-export",
		"snapshot-export",
		"tui",
		"official-account-api",
	}
	for _, cmd := range []control.Command{
		{Title: "Version", Argv: []string{"weicrawl", "--json", "version"}, JSON: true},
		{Title: "Init", Argv: []string{"weicrawl", "--json", "init"}, JSON: true, Mutates: true},
		{Title: "Doctor", Argv: []string{"weicrawl", "--json", "doctor"}, JSON: true},
		{Title: "Metadata", Argv: []string{"weicrawl", "--json", "metadata"}, JSON: true},
		{Title: "Status", Argv: []string{"weicrawl", "--json", "status"}, JSON: true},
		{Title: "Sync", Argv: []string{"weicrawl", "--json", "sync", "--source", "all"}, JSON: true, Mutates: true},
		{Title: "Unlock", Argv: []string{"weicrawl", "--json", "unlock", "status"}, JSON: true},
		{Title: "Search", Argv: []string{"weicrawl", "--json", "search"}, JSON: true},
		{Title: "Profiles", Argv: []string{"weicrawl", "--json", "profiles"}, JSON: true},
		{Title: "Contacts", Argv: []string{"weicrawl", "--json", "contacts"}, JSON: true},
		{Title: "Chats", Argv: []string{"weicrawl", "--json", "chats"}, JSON: true},
		{Title: "Chat Members", Argv: []string{"weicrawl", "--json", "chat-members"}, JSON: true},
		{Title: "Messages", Argv: []string{"weicrawl", "--json", "messages"}, JSON: true},
		{Title: "Message Parts", Argv: []string{"weicrawl", "--json", "message-parts"}, JSON: true},
		{Title: "Message Events", Argv: []string{"weicrawl", "--json", "message-events"}, JSON: true},
		{Title: "Favorites", Argv: []string{"weicrawl", "--json", "favorites"}, JSON: true},
		{Title: "Biz Accounts", Argv: []string{"weicrawl", "--json", "biz-accounts"}, JSON: true},
		{Title: "Articles", Argv: []string{"weicrawl", "--json", "articles"}, JSON: true},
		{Title: "Media", Argv: []string{"weicrawl", "--json", "media"}, JSON: true},
		{Title: "Moments", Argv: []string{"weicrawl", "--json", "moments"}, JSON: true},
		{Title: "Raw Records", Argv: []string{"weicrawl", "--json", "raw-records"}, JSON: true},
		{Title: "Runs", Argv: []string{"weicrawl", "--json", "runs"}, JSON: true},
		{Title: "SQL", Argv: []string{"weicrawl", "--json", "sql"}, JSON: true},
		{Title: "Export", Argv: []string{"weicrawl", "--json", "export"}, JSON: true},
		{Title: "Snapshot", Argv: []string{"weicrawl", "--json", "snapshot"}, JSON: true, Mutates: true},
		{Title: "Import", Argv: []string{"weicrawl", "--json", "import"}, JSON: true, Mutates: true},
		{Title: "TUI", Argv: []string{"weicrawl", "--json", "tui"}, JSON: true},
		{Title: "Completion", Argv: []string{"weicrawl", "completion"}, JSON: false},
	} {
		name := cmd.Argv[len(cmd.Argv)-1]
		if name == "all" {
			name = "sync"
		} else if name == "status" && len(cmd.Argv) >= 3 && cmd.Argv[len(cmd.Argv)-2] == "unlock" {
			name = "unlock"
		}
		m.Commands[name] = cmd
	}
	return m
}

func usage() string {
	return strings.TrimSpace(`weicrawl [global flags] <command> [args]

commands:
  init doctor metadata status sync unlock
  profiles contacts chats chat-members messages message-parts message-events
  favorites biz-accounts articles media moments raw-records runs
  search sql export snapshot import tui completion version

global flags:
  --config <path>  config path
  --db <path>      archive db path
  --profile <id>   profile id or wxid
  --json           JSON output
  --quiet          suppress error output
  --verbose        include extra diagnostics where available`)
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
