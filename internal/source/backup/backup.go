package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vincentkoc/weicrawl/internal/archive"
	"github.com/vincentkoc/weicrawl/internal/source/importer"
)

type Options struct {
	Root      string
	ProfileID string
	Since     string
}

type Result struct {
	RunID              string   `json:"run_id"`
	ProfileID          string   `json:"profile_id"`
	Source             string   `json:"source"`
	Since              string   `json:"since,omitempty"`
	BackupRoot         string   `json:"backup_root"`
	SourceDBCount      int      `json:"source_db_count"`
	ImportedProfiles   int64    `json:"imported_profiles"`
	ImportedContacts   int64    `json:"imported_contacts"`
	ImportedChats      int64    `json:"imported_chats"`
	ImportedMessages   int64    `json:"imported_messages"`
	ImportedParts      int64    `json:"imported_message_parts"`
	ImportedEvents     int64    `json:"imported_message_events"`
	ImportedMedia      int64    `json:"imported_media"`
	ImportedFavorites  int64    `json:"imported_favorites"`
	ImportedMoments    int64    `json:"imported_moments"`
	ImportedRawRecords int64    `json:"imported_raw_records"`
	Warnings           []string `json:"warnings,omitempty"`
}

func Sync(ctx context.Context, arc *archive.Archive, opts Options) (Result, error) {
	started := time.Now().UTC()
	root := filepath.Clean(opts.Root)
	runID := "backup-" + started.Format("20060102T150405.000000000Z")
	info, err := os.Stat(root)
	if err != nil {
		return Result{RunID: runID, Source: "desktop-backup", BackupRoot: root}, fmt.Errorf("stat backup root: %w", err)
	}
	if !info.IsDir() {
		return Result{RunID: runID, Source: "desktop-backup", BackupRoot: root}, fmt.Errorf("backup root is not a directory: %s", root)
	}
	profileID := strings.TrimSpace(opts.ProfileID)
	if profileID == "" {
		profileID = backupProfileID(root)
	}
	files := collectDBFiles(root)
	result := Result{
		RunID:         runID,
		ProfileID:     profileID,
		Source:        "desktop-backup",
		Since:         opts.Since,
		BackupRoot:    root,
		SourceDBCount: len(files),
	}
	if err := arc.UpsertProfile(ctx, profileID, "", filepath.Base(root), root, "", map[string]any{"source": "desktop-backup", "backup_root": root}); err != nil {
		return result, err
	}
	result.ImportedProfiles = 1
	importResult, warnings, err := importer.ImportFixtureDatabasesWithOptions(ctx, arc, profileID, files, importer.Options{Since: opts.Since})
	result.ImportedContacts = importResult.Contacts
	result.ImportedChats = importResult.Chats
	result.ImportedMessages = importResult.Messages
	result.ImportedParts = importResult.MessageParts
	result.ImportedEvents = importResult.MessageEvents
	result.ImportedMedia = importResult.Media
	result.ImportedFavorites = importResult.Favorites
	result.ImportedMoments = importResult.Moments
	result.ImportedRawRecords = importResult.RawRecords
	result.Warnings = append(result.Warnings, warnings...)
	status := "success"
	if len(files) == 0 {
		status = "failed"
		result.Warnings = append(result.Warnings, "no .db files found under backup root")
	}
	if err != nil {
		status = "partial"
		result.Warnings = append(result.Warnings, err.Error())
	}
	if len(files) > 0 && result.ImportedMessages == 0 {
		result.Warnings = append(result.Warnings, "backup DBs were readable but no supported message tables were found")
	}
	if insertErr := arc.InsertSyncRun(ctx, archive.SyncRun{
		RunID:              runID,
		Source:             result.Source,
		ProfileID:          profileID,
		StartedAt:          started.Format(time.RFC3339),
		FinishedAt:         time.Now().UTC().Format(time.RFC3339),
		Status:             status,
		SourceRoot:         root,
		SourceDBCount:      int64(len(files)),
		ImportedProfiles:   result.ImportedProfiles,
		ImportedContacts:   result.ImportedContacts,
		ImportedChats:      result.ImportedChats,
		ImportedMessages:   result.ImportedMessages,
		ImportedMedia:      result.ImportedMedia,
		ImportedRawRecords: result.ImportedRawRecords,
		Warnings:           result.Warnings,
	}); insertErr != nil {
		return result, insertErr
	}
	return result, err
}

func collectDBFiles(root string) []importer.File {
	var files []importer.File
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			return nil
		}
		files = append(files, importer.File{Path: path, Role: roleForPath(root, path)})
		return nil
	})
	return files
}

func roleForPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "backup"
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if part == "db_storage" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	if len(parts) > 1 {
		return parts[len(parts)-2]
	}
	return "backup"
}

func backupProfileID(root string) string {
	base := strings.Trim(filepath.Base(root), ". ")
	if base == "" || base == string(filepath.Separator) {
		base = "backup"
	}
	sum := sha256.Sum256([]byte(root))
	return "backup-" + safeID(base) + "-" + hex.EncodeToString(sum[:])[:8]
}

func safeID(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "backup"
	}
	return out
}
