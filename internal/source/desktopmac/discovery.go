package desktopmac

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	DefaultAppPath      = "/Applications/WeChat.app"
	DefaultBundleID     = "com.tencent.xinWeChat"
	XWeChatRelativeRoot = "Data/Documents/xwechat_files"
)

type Discovery struct {
	SupportedOS      bool      `json:"supported_os"`
	AppPath          string    `json:"app_path,omitempty"`
	BundleID         string    `json:"bundle_id,omitempty"`
	AppVersion       string    `json:"app_version,omitempty"`
	BundleVersion    string    `json:"bundle_version,omitempty"`
	Running          bool      `json:"running"`
	ContainerPath    string    `json:"container_path"`
	XWeChatRoot      string    `json:"xwechat_root,omitempty"`
	AppPresent       bool      `json:"app_present"`
	ContainerPresent bool      `json:"container_present"`
	ProfileRoots     []Profile `json:"profile_roots,omitempty"`
	BackupDirs       []string  `json:"backup_dirs,omitempty"`
	KeyInfoDatabases []DBFile  `json:"key_info_databases,omitempty"`
	DatabaseCount    int       `json:"database_count"`
	EncryptedDBCount int       `json:"encrypted_db_count"`
	KeyInfoDBCount   int       `json:"key_info_db_count"`
	MediaDirCount    int       `json:"media_dir_count"`
	Warnings         []string  `json:"warnings,omitempty"`
}

type Profile struct {
	ProfileID string   `json:"profile_id"`
	Wxid      string   `json:"wxid,omitempty"`
	Root      string   `json:"root"`
	Databases []DBFile `json:"databases,omitempty"`
	MediaDirs []string `json:"media_dirs,omitempty"`
}

type DBFile struct {
	Path      string   `json:"path"`
	Role      string   `json:"role"`
	Size      int64    `json:"size"`
	SQLite    bool     `json:"sqlite"`
	Encrypted bool     `json:"encrypted"`
	Sidecars  []string `json:"sidecars,omitempty"`
}

func Discover(ctx context.Context, containerPath string) Discovery {
	if strings.TrimSpace(containerPath) == "" {
		containerPath = defaultContainerPath()
	}
	out := Discovery{
		SupportedOS:   runtime.GOOS == "darwin",
		AppPath:       DefaultAppPath,
		BundleID:      DefaultBundleID,
		ContainerPath: containerPath,
	}
	if runtime.GOOS != "darwin" {
		out.Warnings = append(out.Warnings, "desktop-macos source is only supported on darwin")
		return out
	}
	if _, err := os.Stat(DefaultAppPath); err == nil {
		out.AppPresent = true
		out.AppVersion = plistValue(ctx, DefaultAppPath, "CFBundleShortVersionString")
		out.BundleVersion = plistValue(ctx, DefaultAppPath, "CFBundleVersion")
		if bundleID := plistValue(ctx, DefaultAppPath, "CFBundleIdentifier"); bundleID != "" {
			out.BundleID = bundleID
		}
		out.Running = processRunning(ctx, DefaultBundleID, "WeChat")
	} else if errors.Is(err, os.ErrNotExist) {
		out.Warnings = append(out.Warnings, "WeChat.app was not found at /Applications/WeChat.app")
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("stat WeChat.app: %v", err))
	}
	if _, err := os.Stat(containerPath); err == nil {
		out.ContainerPresent = true
	} else if errors.Is(err, os.ErrNotExist) {
		out.Warnings = append(out.Warnings, "WeChat container was not found")
		return out
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("stat WeChat container: %v", err))
		return out
	}
	root := filepath.Join(containerPath, XWeChatRelativeRoot)
	out.XWeChatRoot = root
	out.BackupDirs = findBackupDirs(root)
	out.KeyInfoDatabases = findKeyInfoDatabases(root)
	out.KeyInfoDBCount = len(out.KeyInfoDatabases)
	entries, err := os.ReadDir(root)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("read xwechat_files: %v", err))
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "all_users" || name == "Backup" || strings.HasPrefix(name, ".") {
			continue
		}
		profileRoot := filepath.Join(root, name)
		if _, err := os.Stat(filepath.Join(profileRoot, "db_storage")); err != nil {
			continue
		}
		profile := Profile{
			ProfileID: sanitizeProfileID(name),
			Wxid:      wxidFromProfile(name),
			Root:      profileRoot,
		}
		profile.Databases = findDatabases(profileRoot)
		profile.MediaDirs = findMediaDirs(profileRoot)
		out.DatabaseCount += len(profile.Databases)
		for _, db := range profile.Databases {
			if db.Encrypted {
				out.EncryptedDBCount++
			}
		}
		out.MediaDirCount += len(profile.MediaDirs)
		out.ProfileRoots = append(out.ProfileRoots, profile)
	}
	sort.Slice(out.ProfileRoots, func(i, j int) bool {
		return out.ProfileRoots[i].ProfileID < out.ProfileRoots[j].ProfileID
	})
	return out
}

func defaultContainerPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "~/Library/Containers/com.tencent.xinWeChat"
	}
	return filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat")
}

func plistValue(ctx context.Context, appPath, key string) string {
	info := filepath.Join(appPath, "Contents", "Info.plist")
	cmd := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, info)
	bytes, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes))
}

func processRunning(ctx context.Context, bundleID, name string) bool {
	if bundleID != "" {
		cmd := exec.CommandContext(ctx, "pgrep", "-x", bundleID)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	if name != "" {
		cmd := exec.CommandContext(ctx, "pgrep", "-x", name)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func findDatabases(profileRoot string) []DBFile {
	dbRoot := filepath.Join(profileRoot, "db_storage")
	var out []DBFile
	_ = filepath.WalkDir(dbRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".db") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		role := "unknown"
		if rel, err := filepath.Rel(dbRoot, path); err == nil {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > 1 {
				role = parts[0]
			}
		}
		sqliteHeader := hasSQLiteHeader(path)
		db := DBFile{Path: path, Role: role, Size: info.Size(), SQLite: sqliteHeader, Encrypted: !sqliteHeader}
		for _, suffix := range []string{"-wal", "-shm"} {
			sidecar := path + suffix
			if _, err := os.Stat(sidecar); err == nil {
				db.Sidecars = append(db.Sidecars, sidecar)
			}
		}
		out = append(out, db)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func hasSQLiteHeader(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 16)
	n, err := file.Read(header)
	if err != nil || n < len(header) {
		return false
	}
	return string(header) == "SQLite format 3\x00"
}

func findBackupDirs(xwechatRoot string) []string {
	candidates := []string{
		filepath.Join(xwechatRoot, "Backup"),
		filepath.Join(filepath.Dir(xwechatRoot), "Backup"),
	}
	var out []string
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			out = append(out, candidate)
		}
	}
	return out
}

func findMediaDirs(profileRoot string) []string {
	candidates := []string{
		filepath.Join(profileRoot, "msg", "file"),
		filepath.Join(profileRoot, "msg", "video"),
		filepath.Join(profileRoot, "msg", "attach"),
		filepath.Join(profileRoot, "cache"),
	}
	var out []string
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			out = append(out, candidate)
		}
	}
	return out
}

func findKeyInfoDatabases(xwechatRoot string) []DBFile {
	loginRoot := filepath.Join(xwechatRoot, "all_users", "login")
	var out []DBFile
	_ = filepath.WalkDir(loginRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || entry.Name() != "key_info.db" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		sqliteHeader := hasSQLiteHeader(path)
		db := DBFile{Path: path, Role: "key_info", Size: info.Size(), SQLite: sqliteHeader, Encrypted: !sqliteHeader}
		for _, suffix := range []string{"-wal", "-shm"} {
			sidecar := path + suffix
			if _, err := os.Stat(sidecar); err == nil {
				db.Sidecars = append(db.Sidecars, sidecar)
			}
		}
		out = append(out, db)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func sanitizeProfileID(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "_")
	if name == "" {
		return "profile"
	}
	return name
}

func wxidFromProfile(name string) string {
	if strings.HasPrefix(name, "wxid_") {
		if idx := strings.LastIndex(name, "_"); idx > len("wxid_") {
			return name[:idx]
		}
		return name
	}
	if idx := strings.Index(name, "_"); idx > 0 {
		return name[:idx]
	}
	return name
}

func SelectProfile(discovery Discovery, requested string) (Profile, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" && len(discovery.ProfileRoots) == 1 {
		return discovery.ProfileRoots[0], true
	}
	for _, profile := range discovery.ProfileRoots {
		if requested == profile.ProfileID || requested == profile.Wxid || requested == filepath.Base(profile.Root) {
			return profile, true
		}
	}
	return Profile{}, false
}
