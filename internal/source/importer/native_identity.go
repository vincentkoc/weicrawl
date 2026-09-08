package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/vincentkoc/weicrawl/internal/archive"
)

type nativeMessageIdentity struct {
	role, shard, table string
	localID            int64
}

func uniqueSourceFiles(files []File) ([]File, error) {
	type namespace struct{ role, base string }
	seen := make(map[namespace]File)
	out := make([]File, 0, len(files))
	for _, file := range files {
		key := namespace{file.Role, filepath.Base(file.Path)}
		if previous, exists := seen[key]; exists {
			first, firstErr := os.Stat(previous.Path)
			second, secondErr := os.Stat(file.Path)
			if firstErr == nil && secondErr == nil && os.SameFile(first, second) {
				continue
			}
			return nil, fmt.Errorf("ambiguous selected shard %q in role %q", key.base, key.role)
		}
		seen[key] = file
		out = append(out, file)
	}
	return out, nil
}

func (identity nativeMessageIdentity) legacyID() string {
	return identity.role + ":" + identity.table + ":" + strconv.FormatInt(identity.localID, 10)
}

func (identity nativeMessageIdentity) qualifiedID() string {
	data, _ := json.Marshal([4]any{identity.role, identity.shard, identity.table, identity.localID})
	return fmt.Sprintf("native-v2:%x", sha256.Sum256(data))
}

func resolveNativeMessageID(ctx context.Context, arc *archive.Archive, profile string, identity nativeMessageIdentity) (string, error) {
	legacy, err := nativeTargetMatches(ctx, arc, profile, identity.legacyID(), identity, false)
	if err != nil {
		return "", err
	}
	qualified, err := nativeTargetMatches(ctx, arc, profile, identity.qualifiedID(), identity, true)
	if err != nil {
		return "", err
	}
	if legacy && qualified {
		return "", fmt.Errorf("ambiguous legacy and native-v2 messages for shard %q", identity.shard)
	}
	if legacy {
		return identity.legacyID(), nil
	}
	return identity.qualifiedID(), nil
}

func nativeTargetMatches(ctx context.Context, arc *archive.Archive, profile, id string, identity nativeMessageIdentity, qualified bool) (bool, error) {
	var sourceDB, sourceRowID, raw string
	err := arc.DB().QueryRowContext(ctx,
		`select source_db, source_rowid, raw_json from messages where profile_id = ? and message_id = ?`,
		profile, id).Scan(&sourceDB, &sourceRowID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var stored struct {
		Version json.RawMessage `json:"native_identity_version"`
		Shard   string          `json:"source_db"`
		Role    string          `json:"source_role"`
		Table   string          `json:"source_table"`
		LocalID *int64          `json:"local_id"`
	}
	invalid := func() (bool, error) {
		return false, fmt.Errorf("ambiguous native provenance for message %q; retained content was not rekeyed", id)
	}
	// Decode directly into int64: native local IDs can exceed JSON float precision.
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.LocalID == nil {
		return invalid()
	}
	version := 0
	if len(stored.Version) > 0 {
		if err := json.Unmarshal(stored.Version, &version); err != nil || version != 2 {
			return invalid()
		}
	}
	if qualified && version != 2 {
		return invalid()
	}
	if stored.Role != identity.role || stored.Table != identity.table || *stored.LocalID != identity.localID ||
		sourceDB != stored.Role || sourceRowID != stored.Table+":"+strconv.FormatInt(*stored.LocalID, 10) ||
		stored.Shard == "" || stored.Shard == "." || stored.Shard == ".." || filepath.Base(stored.Shard) != stored.Shard {
		return invalid()
	}
	if qualified && stored.Shard != identity.shard {
		return invalid()
	}
	return stored.Shard == identity.shard, nil
}

// Check the selected native key set before message/FTS/part/rich writes.
// This is a read preflight, not a transaction over the file or whole import.
func preflightNativeMessageIDs(ctx context.Context, arc *archive.Archive, db *sql.DB, profile string, file File, usernames []string, tables map[string]bool, opts Options) error {
	for _, username := range usernames {
		table := messageTableName(username)
		if !tables[table] {
			continue
		}
		rows, err := db.QueryContext(ctx, `select local_id, coalesce(create_time,0) from `+quoteIdent(table))
		if err != nil {
			return err
		}
		for rows.Next() {
			var localID, createdAt int64
			if err := rows.Scan(&localID, &createdAt); err != nil {
				_ = rows.Close()
				return err
			}
			if !includeSince(unixSeconds(createdAt), opts.Since) {
				continue
			}
			identity := nativeMessageIdentity{file.Role, filepath.Base(file.Path), table, localID}
			if _, err := resolveNativeMessageID(ctx, arc, profile, identity); err != nil {
				_ = rows.Close()
				return err
			}
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return err
		}
	}
	return nil
}
