package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const DefaultContainerPath = "~/Library/Containers/com.tencent.xinWeChat"

type Config struct {
	Archive         ArchiveConfig         `toml:"archive" json:"archive"`
	DesktopMacOS    DesktopMacOSConfig    `toml:"desktop_macos" json:"desktop_macos"`
	Unlock          UnlockConfig          `toml:"unlock" json:"unlock"`
	OfficialAccount OfficialAccountConfig `toml:"official_account" json:"official_account"`
}

type ArchiveConfig struct {
	DBPath   string `toml:"db_path" json:"db_path"`
	CacheDir string `toml:"cache_dir" json:"cache_dir"`
	LogDir   string `toml:"log_dir" json:"log_dir"`
}

type DesktopMacOSConfig struct {
	Enabled                bool   `toml:"enabled" json:"enabled"`
	ContainerPath          string `toml:"container_path" json:"container_path"`
	SnapshotMode           string `toml:"snapshot_mode" json:"snapshot_mode"`
	MediaMode              string `toml:"media_mode" json:"media_mode"`
	KeepSourceSnapshots    bool   `toml:"keep_source_snapshots" json:"keep_source_snapshots"`
	KeepDecryptedSnapshots bool   `toml:"keep_decrypted_snapshots" json:"keep_decrypted_snapshots"`
}

type UnlockConfig struct {
	AllowProcessInspect bool `toml:"allow_process_inspect" json:"allow_process_inspect"`
	AllowKeychain       bool `toml:"allow_keychain" json:"allow_keychain"`
	StoreKeychain       bool `toml:"store_keychain" json:"store_keychain"`
}

type OfficialAccountConfig struct {
	Enabled      bool   `toml:"enabled" json:"enabled"`
	AppIDEnv     string `toml:"app_id_env" json:"app_id_env"`
	AppSecretEnv string `toml:"app_secret_env" json:"app_secret_env"`
}

type Loaded struct {
	Config Config `json:"config"`
	Path   string `json:"path"`
}

func Default() Config {
	return Config{
		Archive: ArchiveConfig{
			DBPath:   "~/.config/weicrawl/weicrawl.db",
			CacheDir: "~/.cache/weicrawl",
			LogDir:   "~/.local/state/weicrawl/logs",
		},
		DesktopMacOS: DesktopMacOSConfig{
			Enabled:       true,
			ContainerPath: DefaultContainerPath,
			SnapshotMode:  "copy",
			MediaMode:     "metadata",
		},
		OfficialAccount: OfficialAccountConfig{
			AppIDEnv:     "WEICRAWL_WECHAT_APP_ID",
			AppSecretEnv: "WEICRAWL_WECHAT_APP_SECRET",
		},
	}
}

func DefaultPath() string {
	if value := strings.TrimSpace(os.Getenv("WEICRAWL_CONFIG")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "weicrawl", "config.toml")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return "weicrawl.toml"
	}
	return filepath.Join(home, ".config", "weicrawl", "config.toml")
}

func Load(path string) (Loaded, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = Expand(path)
	cfg := Default()
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			applyEnv(&cfg)
			expandConfig(&cfg)
			return Loaded{Config: cfg, Path: path}, nil
		}
		return Loaded{}, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(bytes, &cfg); err != nil {
		return Loaded{}, fmt.Errorf("parse config: %w", err)
	}
	applyEnv(&cfg)
	expandConfig(&cfg)
	return Loaded{Config: cfg, Path: path}, nil
}

func WriteDefault(path string, overwrite bool) (Loaded, bool, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = Expand(path)
	if _, err := os.Stat(path); err == nil && !overwrite {
		loaded, loadErr := Load(path)
		return loaded, false, loadErr
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Loaded{}, false, fmt.Errorf("stat config: %w", err)
	}
	cfg := Default()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Loaded{}, false, fmt.Errorf("create config dir: %w", err)
	}
	bytes, err := toml.Marshal(cfg)
	if err != nil {
		return Loaded{}, false, fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return Loaded{}, false, fmt.Errorf("write config: %w", err)
	}
	loaded, err := Load(path)
	return loaded, true, err
}

func Expand(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return os.ExpandEnv(path)
}

func applyEnv(cfg *Config) {
	if value := strings.TrimSpace(os.Getenv("WEICRAWL_DB_PATH")); value != "" {
		cfg.Archive.DBPath = value
	}
	if value := strings.TrimSpace(os.Getenv("WEICRAWL_CACHE_DIR")); value != "" {
		cfg.Archive.CacheDir = value
	}
}

func expandConfig(cfg *Config) {
	cfg.Archive.DBPath = Expand(cfg.Archive.DBPath)
	cfg.Archive.CacheDir = Expand(cfg.Archive.CacheDir)
	cfg.Archive.LogDir = Expand(cfg.Archive.LogDir)
	cfg.DesktopMacOS.ContainerPath = Expand(cfg.DesktopMacOS.ContainerPath)
	if strings.TrimSpace(cfg.DesktopMacOS.SnapshotMode) == "" {
		cfg.DesktopMacOS.SnapshotMode = "copy"
	}
	if strings.TrimSpace(cfg.DesktopMacOS.MediaMode) == "" {
		cfg.DesktopMacOS.MediaMode = "metadata"
	}
	if strings.TrimSpace(cfg.OfficialAccount.AppIDEnv) == "" {
		cfg.OfficialAccount.AppIDEnv = "WEICRAWL_WECHAT_APP_ID"
	}
	if strings.TrimSpace(cfg.OfficialAccount.AppSecretEnv) == "" {
		cfg.OfficialAccount.AppSecretEnv = "WEICRAWL_WECHAT_APP_SECRET"
	}
}
