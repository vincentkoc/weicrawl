package unlock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var hexKeyRE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type KeyManifest struct {
	Keys map[string]string `json:"keys"`
}

type DecryptOptions struct {
	SnapshotDir   string
	OutputDir     string
	KeysPath      string
	SQLCipherPath string
}

type DecryptResult struct {
	SnapshotDir string         `json:"snapshot_dir"`
	OutputDir   string         `json:"output_dir"`
	SQLCipher   string         `json:"sqlcipher"`
	Decrypted   []DecryptEntry `json:"decrypted,omitempty"`
	Skipped     []DecryptEntry `json:"skipped,omitempty"`
}

type DecryptEntry struct {
	Database string `json:"database"`
	Output   string `json:"output,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type KeyReadiness struct {
	SnapshotDir string         `json:"snapshot_dir"`
	KeysPath    string         `json:"keys_path"`
	SQLCipher   string         `json:"sqlcipher,omitempty"`
	KeyCount    int            `json:"key_count"`
	Found       []DecryptEntry `json:"found,omitempty"`
	Missing     []DecryptEntry `json:"missing,omitempty"`
	Ready       bool           `json:"ready"`
}

type KeyScanPlan struct {
	Allowed bool     `json:"allowed"`
	Execute bool     `json:"execute"`
	Command []string `json:"command"`
	Notes   []string `json:"notes,omitempty"`
}

func BuildKeyScanPlan(allowProcessInspect, execute bool, scriptPath, outputPath string) (KeyScanPlan, error) {
	plan := KeyScanPlan{
		Allowed: allowProcessInspect,
		Execute: execute,
		Notes: []string{
			"requires WeChat running",
			"may require SIP/debug permissions depending on macOS setup",
			"writes a wechat_keys.json-style manifest; do not commit it",
		},
	}
	if !allowProcessInspect {
		return plan, errors.New("refusing process inspection without --allow-process-inspect")
	}
	if strings.TrimSpace(scriptPath) == "" {
		scriptPath = "find_key_memscan.py"
	}
	if strings.TrimSpace(outputPath) == "" {
		outputPath = "wechat_keys.json"
	}
	plan.Command = []string{"python3", scriptPath}
	plan.Notes = append(plan.Notes, "run from the key extractor directory or pass --script")
	plan.Notes = append(plan.Notes, "expected output: "+outputPath)
	return plan, nil
}

func ExecuteKeyScan(ctx context.Context, plan KeyScanPlan) ([]byte, error) {
	if !plan.Allowed {
		return nil, errors.New("key scan is not allowed")
	}
	if len(plan.Command) == 0 {
		return nil, errors.New("key scan command is empty")
	}
	cmd := exec.CommandContext(ctx, plan.Command[0], plan.Command[1:]...)
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

func ReadKeyManifest(path string) (KeyManifest, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return KeyManifest{}, fmt.Errorf("read key manifest: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes, &raw); err != nil {
		return KeyManifest{}, fmt.Errorf("parse key manifest: %w", err)
	}
	keys := map[string]string{}
	for key, value := range raw {
		if strings.HasPrefix(key, "__") {
			continue
		}
		stringValue, ok := value.(string)
		if !ok {
			continue
		}
		stringValue = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(stringValue), "x'"), "0x")
		stringValue = strings.TrimSuffix(stringValue, "'")
		if !hexKeyRE.MatchString(stringValue) {
			return KeyManifest{}, fmt.Errorf("invalid key for %s", key)
		}
		keys[filepath.Clean(key)] = strings.ToLower(stringValue)
	}
	if len(keys) == 0 {
		return KeyManifest{}, errors.New("key manifest did not contain database keys")
	}
	return KeyManifest{Keys: keys}, nil
}

func DecryptSnapshot(ctx context.Context, opts DecryptOptions) (DecryptResult, error) {
	if strings.TrimSpace(opts.SnapshotDir) == "" {
		return DecryptResult{}, errors.New("snapshot dir is required")
	}
	if strings.TrimSpace(opts.OutputDir) == "" {
		return DecryptResult{}, errors.New("output dir is required")
	}
	keys, err := ReadKeyManifest(opts.KeysPath)
	if err != nil {
		return DecryptResult{}, err
	}
	sqlcipher, err := FindSQLCipher(opts.SQLCipherPath)
	if err != nil {
		return DecryptResult{}, err
	}
	result := DecryptResult{SnapshotDir: opts.SnapshotDir, OutputDir: opts.OutputDir, SQLCipher: sqlcipher}
	dbRoot := filepath.Join(opts.SnapshotDir, "db_storage")
	if _, err := os.Stat(dbRoot); err != nil {
		return result, fmt.Errorf("stat snapshot db_storage: %w", err)
	}
	relPaths := make([]string, 0, len(keys.Keys))
	for rel := range keys.Keys {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)
	for _, rel := range relPaths {
		src := filepath.Join(dbRoot, rel)
		if _, err := os.Stat(src); err != nil {
			result.Skipped = append(result.Skipped, DecryptEntry{Database: rel, Reason: "source not found"})
			continue
		}
		dst := filepath.Join(opts.OutputDir, rel)
		if err := decryptOne(ctx, sqlcipher, src, dst, keys.Keys[rel]); err != nil {
			result.Skipped = append(result.Skipped, DecryptEntry{Database: rel, Reason: err.Error()})
			continue
		}
		result.Decrypted = append(result.Decrypted, DecryptEntry{Database: rel, Output: dst})
	}
	if len(result.Decrypted) == 0 {
		return result, errors.New("no databases were decrypted")
	}
	return result, nil
}

func CheckSnapshotKeys(opts DecryptOptions) (KeyReadiness, error) {
	if strings.TrimSpace(opts.SnapshotDir) == "" {
		return KeyReadiness{}, errors.New("snapshot dir is required")
	}
	keys, err := ReadKeyManifest(opts.KeysPath)
	if err != nil {
		return KeyReadiness{}, err
	}
	sqlcipher, err := FindSQLCipher(opts.SQLCipherPath)
	if err != nil {
		return KeyReadiness{}, err
	}
	result := KeyReadiness{SnapshotDir: opts.SnapshotDir, KeysPath: opts.KeysPath, SQLCipher: sqlcipher, KeyCount: len(keys.Keys)}
	dbRoot := filepath.Join(opts.SnapshotDir, "db_storage")
	if _, err := os.Stat(dbRoot); err != nil {
		return result, fmt.Errorf("stat snapshot db_storage: %w", err)
	}
	relPaths := make([]string, 0, len(keys.Keys))
	for rel := range keys.Keys {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)
	for _, rel := range relPaths {
		src := filepath.Join(dbRoot, rel)
		if info, err := os.Stat(src); err != nil || info.IsDir() {
			result.Missing = append(result.Missing, DecryptEntry{Database: rel, Reason: "source not found"})
			continue
		}
		result.Found = append(result.Found, DecryptEntry{Database: rel})
	}
	result.Ready = len(result.Found) > 0 && len(result.Missing) == 0
	return result, nil
}

func FindSQLCipher(configured string) (string, error) {
	candidates := []string{}
	if strings.TrimSpace(configured) != "" {
		candidates = append(candidates, configured)
	}
	if env := strings.TrimSpace(os.Getenv("WEICRAWL_SQLCIPHER")); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "/opt/homebrew/opt/sqlcipher/bin/sqlcipher", "/usr/local/opt/sqlcipher/bin/sqlcipher")
	if path, err := exec.LookPath("sqlcipher"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("sqlcipher not found; install sqlcipher or set WEICRAWL_SQLCIPHER")
}

func decryptOne(ctx context.Context, sqlcipher, src, dst, keyHex string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_ = os.Remove(dst)
	commands := fmt.Sprintf(`PRAGMA key = "x'%s'";
PRAGMA cipher_page_size = 4096;
ATTACH DATABASE '%s' AS plaintext KEY '';
SELECT sqlcipher_export('plaintext');
DETACH DATABASE plaintext;
`, keyHex, strings.ReplaceAll(dst, `'`, `''`))
	cmd := exec.CommandContext(ctx, sqlcipher, src)
	cmd.Stdin = strings.NewReader(commands)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlcipher decrypt failed: %s", strings.TrimSpace(string(output)))
	}
	if info, err := os.Stat(dst); err != nil {
		return fmt.Errorf("decrypted output missing: %w", err)
	} else if info.Size() == 0 {
		return errors.New("decrypted output is empty")
	}
	return nil
}
