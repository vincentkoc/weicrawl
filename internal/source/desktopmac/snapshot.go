package desktopmac

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/source/importer"
)

type SnapshotOptions struct {
	CacheDir     string
	Profile      Profile
	AppVersion   string
	IncludeMedia bool
	MediaMode    string
	Keep         bool
	Since        string
	Concurrency  int
}

type Snapshot struct {
	RunID              string            `json:"run_id"`
	Root               string            `json:"root"`
	ProfileID          string            `json:"profile_id"`
	Wxid               string            `json:"wxid,omitempty"`
	AppVersion         string            `json:"app_version,omitempty"`
	DatabaseFiles      []SnapshotFile    `json:"database_files"`
	KeyInfoFiles       []SnapshotFile    `json:"key_info_files,omitempty"`
	MediaDirs          []string          `json:"media_dirs,omitempty"`
	SourceFingerprints map[string]string `json:"source_fingerprints,omitempty"`
}

type SnapshotFile struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Role   string `json:"role"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

type SyncResult struct {
	RunID               string   `json:"run_id"`
	ProfileID           string   `json:"profile_id,omitempty"`
	Source              string   `json:"source"`
	Status              string   `json:"status"`
	Since               string   `json:"since,omitempty"`
	Concurrency         int      `json:"concurrency,omitempty"`
	SnapshotPath        string   `json:"snapshot_path,omitempty"`
	DecryptedSnapshot   string   `json:"decrypted_snapshot_path,omitempty"`
	SourceDBCount       int      `json:"source_db_count"`
	KeyInfoDBCount      int      `json:"key_info_db_count,omitempty"`
	ImportedProfiles    int64    `json:"imported_profiles"`
	ImportedContacts    int64    `json:"imported_contacts"`
	ImportedChats       int64    `json:"imported_chats"`
	ImportedMessages    int64    `json:"imported_messages"`
	ImportedParts       int64    `json:"imported_message_parts"`
	ImportedEvents      int64    `json:"imported_message_events"`
	ImportedMedia       int64    `json:"imported_media"`
	ImportedBizAccounts int64    `json:"imported_biz_accounts"`
	ImportedArticles    int64    `json:"imported_articles"`
	ImportedFavorites   int64    `json:"imported_favorites"`
	ImportedMoments     int64    `json:"imported_moments"`
	ImportedRawRecords  int64    `json:"imported_raw_records"`
	Warnings            []string `json:"warnings,omitempty"`
}

func CreateSnapshot(ctx context.Context, opts SnapshotOptions) (snap Snapshot, resultErr error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(opts.CacheDir) == "" {
		return Snapshot{}, fmt.Errorf("cache dir is required")
	}
	if strings.TrimSpace(opts.Profile.Root) == "" {
		return Snapshot{}, fmt.Errorf("profile root is required")
	}
	if opts.Profile.ProfileID == "." || !filepath.IsLocal(opts.Profile.ProfileID) || filepath.Base(opts.Profile.ProfileID) != opts.Profile.ProfileID {
		return Snapshot{}, fmt.Errorf("profile id must be a directory name")
	}
	parent := filepath.Join(opts.CacheDir, "snapshots")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Snapshot{}, err
	}
	owned, err := os.MkdirTemp(parent, "sync-*")
	if err != nil {
		return Snapshot{}, err
	}
	defer func() {
		if resultErr != nil && !opts.Keep {
			if err := os.RemoveAll(owned); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("cleanup source snapshot: %w", err))
			} else {
				snap.Root = ""
			}
		}
	}()
	runID := filepath.Base(owned)
	root := filepath.Join(owned, opts.Profile.ProfileID)
	dbDir := filepath.Join(root, "db_storage")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot dir: %w", err)
	}
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	snap = Snapshot{
		RunID:              runID,
		Root:               root,
		ProfileID:          opts.Profile.ProfileID,
		Wxid:               opts.Profile.Wxid,
		AppVersion:         opts.AppVersion,
		SourceFingerprints: map[string]string{},
	}
	for _, db := range opts.Profile.Databases {
		rel, err := filepath.Rel(filepath.Join(opts.Profile.Root, "db_storage"), db.Path)
		if err != nil {
			rel = filepath.Base(db.Path)
		}
		dst := filepath.Join(dbDir, rel)
		if !filepath.IsLocal(rel) {
			return snap, fmt.Errorf("database escapes selected source root")
		}
		if err := copyFile(ctx, dst, db.Path); err != nil {
			return snap, fmt.Errorf("copy db %s: %w", db.Path, err)
		}
		hash, err := fileSHA256(ctx, dst)
		if err != nil {
			return snap, err
		}
		snap.SourceFingerprints[rel] = hash
		info, _ := os.Stat(dst)
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		snap.DatabaseFiles = append(snap.DatabaseFiles, SnapshotFile{Source: db.Path, Path: dst, Role: db.Role, Size: size, SHA256: hash})
		for _, sidecar := range db.Sidecars {
			sideRel, err := filepath.Rel(filepath.Join(opts.Profile.Root, "db_storage"), sidecar)
			if err != nil {
				sideRel = rel + strings.TrimPrefix(sidecar, db.Path)
			}
			if !filepath.IsLocal(sideRel) {
				return snap, fmt.Errorf("sidecar escapes selected source root")
			}
			if err := copyFile(ctx, filepath.Join(dbDir, sideRel), sidecar); err != nil {
				return snap, fmt.Errorf("copy sidecar %s: %w", sidecar, err)
			}
		}
	}
	for _, db := range opts.Profile.KeyInfoDatabases {
		loginID := filepath.Base(filepath.Dir(db.Path))
		if strings.TrimSpace(loginID) == "" || loginID == "." || loginID == string(filepath.Separator) {
			loginID = "unknown"
		}
		rel := filepath.Join(loginID, filepath.Base(db.Path))
		dst := filepath.Join(root, "key_info", rel)
		if err := copyFile(ctx, dst, db.Path); err != nil {
			return snap, fmt.Errorf("copy key info db %s: %w", db.Path, err)
		}
		hash, err := fileSHA256(ctx, dst)
		if err != nil {
			return snap, err
		}
		snap.SourceFingerprints[filepath.Join("key_info", rel)] = hash
		info, _ := os.Stat(dst)
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		snap.KeyInfoFiles = append(snap.KeyInfoFiles, SnapshotFile{Source: db.Path, Path: dst, Role: db.Role, Size: size, SHA256: hash})
		for _, sidecar := range db.Sidecars {
			sideRel := rel + strings.TrimPrefix(sidecar, db.Path)
			if err := copyFile(ctx, filepath.Join(root, "key_info", sideRel), sidecar); err != nil {
				return snap, fmt.Errorf("copy key info sidecar %s: %w", sidecar, err)
			}
		}
	}
	if opts.IncludeMedia {
		mediaRoot := filepath.Join(root, "media")
		for _, dir := range opts.Profile.MediaDirs {
			rel, err := filepath.Rel(opts.Profile.Root, dir)
			if err != nil {
				rel = filepath.Base(dir)
			}
			dst := filepath.Join(mediaRoot, rel)
			if !filepath.IsLocal(rel) {
				return snap, fmt.Errorf("media directory escapes selected source root")
			}
			if opts.MediaMode == "copy" {
				err = copyDir(ctx, dst, dir, concurrency)
			} else {
				err = copyDirMetadataOnly(ctx, dst, dir, concurrency)
			}
			if err != nil {
				return snap, err
			}
			snap.MediaDirs = append(snap.MediaDirs, dst)
		}
	}
	return snap, ctx.Err()
}

func SyncDesktopSnapshot(ctx context.Context, arc *archive.Archive, opts SnapshotOptions) (SyncResult, error) {
	return syncDesktopSnapshot(ctx, arc, opts, os.RemoveAll)
}

func syncDesktopSnapshot(ctx context.Context, arc *archive.Archive, opts SnapshotOptions, remove func(string) error) (result SyncResult, resultErr error) {
	started := time.Now().UTC()
	snap, err := CreateSnapshot(ctx, opts)
	result = SyncResult{Source: "desktop-macos", Since: opts.Since}
	cleanupAttempted := false
	cleanup := func() error {
		if cleanupAttempted || snap.Root == "" || opts.Keep {
			return nil
		}
		cleanupAttempted = true
		if err := remove(filepath.Dir(snap.Root)); err != nil {
			result.Status = "partial"
			err = fmt.Errorf("cleanup source snapshot: %w", err)
			result.Warnings = append(result.Warnings, err.Error())
			return err
		}
		result.SnapshotPath = ""
		return nil
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	if opts.Concurrency > 1 {
		result.Concurrency = opts.Concurrency
	}
	if snap.RunID != "" {
		result.RunID = snap.RunID
		result.ProfileID = snap.ProfileID
		result.SnapshotPath = snap.Root
		result.SourceDBCount = len(snap.DatabaseFiles)
		result.KeyInfoDBCount = len(snap.KeyInfoFiles)
	}
	if err != nil {
		if result.RunID == "" {
			result.RunID = "sync-" + started.Format("20060102T150405.000000000Z")
		}
		result.Status = "failed"
		_ = arc.InsertSyncRun(ctx, archive.SyncRun{RunID: result.RunID, Source: "desktop-macos", ProfileID: opts.Profile.ProfileID, StartedAt: started.Format(time.RFC3339), FinishedAt: time.Now().UTC().Format(time.RFC3339), Status: "failed", AppVersion: opts.AppVersion, SourceRoot: opts.Profile.Root, Warnings: []string{err.Error()}})
		return result, err
	}
	if err := arc.UpsertProfile(ctx, snap.ProfileID, snap.Wxid, "", opts.Profile.Root, opts.AppVersion, snap); err != nil {
		return result, err
	}
	result.ImportedProfiles = 1
	files := make([]importer.File, 0, len(snap.DatabaseFiles))
	for _, file := range snap.DatabaseFiles {
		files = append(files, importer.File{Path: file.Path, Role: file.Role})
	}
	importResult, warnings, err := importer.ImportFixtureDatabasesWithOptions(ctx, arc, snap.ProfileID, files, importer.Options{Since: opts.Since})
	result.ImportedContacts = importResult.Contacts
	result.ImportedChats = importResult.Chats
	result.ImportedMessages = importResult.Messages
	result.ImportedParts = importResult.MessageParts
	result.ImportedEvents = importResult.MessageEvents
	result.ImportedMedia = importResult.Media
	result.ImportedBizAccounts = importResult.BizAccounts
	result.ImportedArticles = importResult.Articles
	result.ImportedFavorites = importResult.Favorites
	result.ImportedMoments = importResult.Moments
	result.ImportedRawRecords = importResult.RawRecords
	mediaCount, mediaErr := ImportMediaMetadata(ctx, arc, snap.ProfileID, snap.MediaDirs)
	result.ImportedMedia += mediaCount
	if mediaErr != nil {
		result.Warnings = append(result.Warnings, mediaErr.Error())
	}
	result.Warnings = append(result.Warnings, warnings...)
	status := "success"
	if err != nil {
		status = "partial"
		result.Warnings = append(result.Warnings, err.Error())
	}
	if mediaErr != nil {
		status = "partial"
	}
	if len(snap.DatabaseFiles) > 0 && result.ImportedMessages == 0 {
		status = "partial"
		result.Warnings = append(result.Warnings, "no readable supported message tables were imported; source DBs may be encrypted or unsupported")
	}
	result.Status = status
	err = errors.Join(err, mediaErr, cleanup())
	finished := time.Now().UTC()
	if err := arc.InsertSyncRun(ctx, archive.SyncRun{
		RunID:               snap.RunID,
		Source:              "desktop-macos",
		ProfileID:           snap.ProfileID,
		StartedAt:           started.Format(time.RFC3339),
		FinishedAt:          finished.Format(time.RFC3339),
		Status:              result.Status,
		AppVersion:          opts.AppVersion,
		SourceRoot:          opts.Profile.Root,
		SnapshotPath:        result.SnapshotPath,
		SourceDBCount:       int64(len(snap.DatabaseFiles)),
		ImportedProfiles:    result.ImportedProfiles,
		ImportedContacts:    result.ImportedContacts,
		ImportedChats:       result.ImportedChats,
		ImportedMessages:    result.ImportedMessages,
		ImportedParts:       result.ImportedParts,
		ImportedEvents:      result.ImportedEvents,
		ImportedMedia:       result.ImportedMedia,
		ImportedBizAccounts: result.ImportedBizAccounts,
		ImportedArticles:    result.ImportedArticles,
		ImportedFavorites:   result.ImportedFavorites,
		ImportedMoments:     result.ImportedMoments,
		ImportedRawRecords:  result.ImportedRawRecords,
		Warnings:            result.Warnings,
	}); err != nil {
		return result, err
	}
	return result, err
}

func SyncDecryptedDirectory(ctx context.Context, arc *archive.Archive, profileID, decryptedDir, appVersion, since string, keepDecrypted bool) (SyncResult, error) {
	started := time.Now().UTC()
	runID := "decrypted-" + started.Format("20060102T150405.000000000Z")
	if strings.TrimSpace(profileID) == "" {
		profileID = filepath.Base(filepath.Clean(decryptedDir))
		if profileID == "." || profileID == string(filepath.Separator) || profileID == "" {
			profileID = "decrypted"
		}
	}
	files := collectDBFiles(decryptedDir)
	result := SyncResult{
		RunID:         runID,
		ProfileID:     profileID,
		Source:        "desktop-macos-decrypted",
		Since:         since,
		SnapshotPath:  decryptedDir,
		SourceDBCount: len(files),
	}
	if keepDecrypted {
		result.DecryptedSnapshot = decryptedDir
	}
	if err := arc.UpsertProfile(ctx, profileID, "", "", decryptedDir, appVersion, map[string]any{"source": "decrypted-dir", "path": decryptedDir}); err != nil {
		return result, err
	}
	result.ImportedProfiles = 1
	importResult, warnings, err := importer.ImportFixtureDatabasesWithOptions(ctx, arc, profileID, files, importer.Options{Since: since})
	result.ImportedContacts = importResult.Contacts
	result.ImportedChats = importResult.Chats
	result.ImportedMessages = importResult.Messages
	result.ImportedParts = importResult.MessageParts
	result.ImportedEvents = importResult.MessageEvents
	result.ImportedMedia = importResult.Media
	result.ImportedBizAccounts = importResult.BizAccounts
	result.ImportedArticles = importResult.Articles
	result.ImportedFavorites = importResult.Favorites
	result.ImportedMoments = importResult.Moments
	result.ImportedRawRecords = importResult.RawRecords
	result.Warnings = append(result.Warnings, warnings...)
	status := "success"
	if err != nil {
		status = "partial"
		result.Warnings = append(result.Warnings, err.Error())
	}
	if len(files) == 0 {
		status = "failed"
		result.Warnings = append(result.Warnings, "no decrypted .db files found")
	}
	if len(files) > 0 && result.ImportedMessages == 0 {
		status = "partial"
		result.Warnings = append(result.Warnings, "decrypted DBs were readable but no supported WeChat message tables were found")
	}
	result.Status = status
	if err := arc.InsertSyncRun(ctx, archive.SyncRun{
		RunID:               runID,
		Source:              result.Source,
		ProfileID:           profileID,
		StartedAt:           started.Format(time.RFC3339),
		FinishedAt:          time.Now().UTC().Format(time.RFC3339),
		Status:              status,
		AppVersion:          appVersion,
		SourceRoot:          decryptedDir,
		SnapshotPath:        decryptedDir,
		SourceDBCount:       int64(len(files)),
		ImportedProfiles:    result.ImportedProfiles,
		ImportedContacts:    result.ImportedContacts,
		ImportedChats:       result.ImportedChats,
		ImportedMessages:    result.ImportedMessages,
		ImportedParts:       result.ImportedParts,
		ImportedEvents:      result.ImportedEvents,
		ImportedMedia:       result.ImportedMedia,
		ImportedBizAccounts: result.ImportedBizAccounts,
		ImportedArticles:    result.ImportedArticles,
		ImportedFavorites:   result.ImportedFavorites,
		ImportedMoments:     result.ImportedMoments,
		ImportedRawRecords:  result.ImportedRawRecords,
		Warnings:            result.Warnings,
	}); err != nil {
		return result, err
	}
	return result, err
}

func collectDBFiles(root string) []importer.File {
	var files []importer.File
	dbRoot := filepath.Join(root, "db_storage")
	if _, err := os.Stat(dbRoot); err != nil {
		dbRoot = root
	}
	_ = filepath.WalkDir(dbRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			return nil
		}
		role := "unknown"
		if rel, err := filepath.Rel(dbRoot, path); err == nil {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > 1 {
				role = parts[0]
			}
		}
		files = append(files, importer.File{Path: path, Role: role})
		return nil
	})
	return files
}

func ImportMediaMetadata(ctx context.Context, arc *archive.Archive, profileID string, mediaDirs []string) (int64, error) {
	var count int64
	for _, root := range mediaDirs {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = filepath.Base(path)
			}
			size := int64(0)
			sourcePath := rel
			archivePath := path
			if strings.HasSuffix(entry.Name(), ".metadata") {
				bytes, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				parts := strings.SplitN(strings.TrimSpace(string(bytes)), "\t", 3)
				if len(parts) < 2 {
					return nil
				}
				rel = parts[0]
				sourcePath = rel
				size, _ = strconv.ParseInt(parts[1], 10, 64)
				archivePath = ""
			} else {
				info, err := entry.Info()
				if err != nil {
					return nil
				}
				size = info.Size()
			}
			raw := map[string]any{"metadata_path": path, "relative_path": rel}
			rawJSON, _ := json.Marshal(raw)
			kind := mediaKind(rel)
			sum := sha256.Sum256([]byte(profileID + "\x00" + rel))
			mediaID := hex.EncodeToString(sum[:])
			if err := arc.UpsertMedia(ctx, archive.MediaItem{
				ProfileID:   profileID,
				MediaID:     mediaID,
				Kind:        kind,
				SourcePath:  sourcePath,
				ArchivePath: archivePath,
				ByteSize:    size,
				RawJSON:     string(rawJSON),
			}); err != nil {
				return err
			}
			count++
			return nil
		})
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

type fileCopyTask struct {
	src      string
	dst      string
	metadata []byte
}

func copyDir(ctx context.Context, dst, src string, concurrency int) error {
	var tasks []fileCopyTask
	if err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry == nil || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return nil
		}
		tasks = append(tasks, fileCopyTask{src: path, dst: filepath.Join(dst, rel)})
		return nil
	}); err != nil {
		return err
	}
	return runCopyTasks(ctx, tasks, concurrency)
}

func mediaKind(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "video") || strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".mov"):
		return "video"
	case strings.Contains(lower, "image") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".webp"):
		return "image"
	case strings.Contains(lower, "voice") || strings.HasSuffix(lower, ".amr") || strings.HasSuffix(lower, ".silk") || strings.HasSuffix(lower, ".mp3"):
		return "voice"
	default:
		return "file"
	}
}

func copyFile(ctx context.Context, dst, src string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, contextReader{ctx, in}); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyDirMetadataOnly(ctx context.Context, dst, src string, concurrency int) error {
	var tasks []fileCopyTask
	if err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry == nil || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		meta := []byte(fmt.Sprintf("%s\t%d\t%s\n", rel, info.Size(), info.ModTime().UTC().Format(time.RFC3339)))
		metaPath := filepath.Join(dst, rel+".metadata")
		tasks = append(tasks, fileCopyTask{dst: metaPath, metadata: meta})
		return nil
	}); err != nil {
		return err
	}
	return runCopyTasks(ctx, tasks, concurrency)
}

func runCopyTasks(ctx context.Context, tasks []fileCopyTask, concurrency int) error {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency == 1 || len(tasks) <= 1 {
		for _, task := range tasks {
			if err := runCopyTask(ctx, task); err != nil {
				return err
			}
		}
		return nil
	}
	if concurrency > len(tasks) {
		concurrency = len(tasks)
	}
	taskCh := make(chan fileCopyTask)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				if err := runCopyTask(ctx, task); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)
	wg.Wait()
	return firstErr
}

func runCopyTask(ctx context.Context, task fileCopyTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task.metadata != nil {
		if err := os.MkdirAll(filepath.Dir(task.dst), 0o700); err != nil {
			return err
		}
		return os.WriteFile(task.dst, task.metadata, 0o600)
	}
	return copyFile(ctx, task.dst, task.src)
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), ctx.Err()
}
