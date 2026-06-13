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
var scanKeyRE = regexp.MustCompile(`(?i)(?:0x|x')?([0-9a-f]{64})'?`)

type KeyManifest struct {
	Keys       map[string]string `json:"keys"`
	DefaultKey string            `json:"-"`
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
	DefaultKey  bool           `json:"default_key,omitempty"`
	Found       []DecryptEntry `json:"found,omitempty"`
	Missing     []DecryptEntry `json:"missing,omitempty"`
	Probed      []DecryptEntry `json:"probed,omitempty"`
	ProbeFailed []DecryptEntry `json:"probe_failed,omitempty"`
	ProbeReady  bool           `json:"probe_ready,omitempty"`
	Ready       bool           `json:"ready"`
}

type KeyScanPlan struct {
	Allowed    bool     `json:"allowed"`
	Execute    bool     `json:"execute"`
	Command    []string `json:"command"`
	OutputPath string   `json:"output_path"`
	Notes      []string `json:"notes,omitempty"`
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
	plan.Command = keyScanCommand(scriptPath)
	plan.OutputPath = outputPath
	plan.Notes = append(plan.Notes, "run from the key extractor directory or pass --script")
	plan.Notes = append(plan.Notes, "expected output: "+outputPath)
	return plan, nil
}

func keyScanCommand(scriptPath string) []string {
	if strings.HasSuffix(strings.ToLower(scriptPath), ".py") {
		return []string{"python3", scriptPath}
	}
	return []string{scriptPath}
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
	if strings.TrimSpace(plan.OutputPath) != "" {
		cmd.Env = append(cmd.Env, "WEICRAWL_SCAN_OUT="+plan.OutputPath, "WEICRAWL_KEY_MANIFEST="+plan.OutputPath)
	}
	return cmd.CombinedOutput()
}

func WriteDefaultKeyManifestFromScan(output []byte, outputPath string) (bool, error) {
	if strings.TrimSpace(outputPath) == "" {
		outputPath = "wechat_keys.json"
	}
	if _, err := ReadKeyManifest(outputPath); err == nil {
		return false, nil
	}
	if manifestBytes, ok := stdoutManifest(output); ok {
		if dir := filepath.Dir(outputPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return false, err
			}
		}
		bytes := append(manifestBytes, '\n')
		if err := os.WriteFile(outputPath, bytes, 0o600); err != nil {
			return false, fmt.Errorf("write key manifest: %w", err)
		}
		return true, nil
	}
	match := scanKeyRE.FindSubmatch(output)
	if len(match) >= 2 {
		key, err := normalizeManifestKey("__default_key", string(match[1]))
		if err != nil {
			return false, err
		}
		if dir := filepath.Dir(outputPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return false, err
			}
		}
		bytes, err := json.MarshalIndent(map[string]string{"__default_key": key}, "", "  ")
		if err != nil {
			return false, err
		}
		bytes = append(bytes, '\n')
		if err := os.WriteFile(outputPath, bytes, 0o600); err != nil {
			return false, fmt.Errorf("write key manifest: %w", err)
		}
		return true, nil
	}
	return false, errors.New("key scan output did not include a 64-hex key and no valid manifest was written")
}

func stdoutManifest(output []byte) ([]byte, bool) {
	trimmed := strings.TrimSpace(string(output))
	if _, err := readKeyManifestBytes([]byte(trimmed)); err == nil {
		return []byte(trimmed), true
	}
scan:
	for start := 0; start < len(output); start++ {
		if output[start] != '{' {
			continue
		}
		depth := 0
		inString := false
		escaped := false
		for end := start; end < len(output); end++ {
			ch := output[end]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == '"' {
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					candidate := []byte(strings.TrimSpace(string(output[start : end+1])))
					if _, err := readKeyManifestBytes(candidate); err == nil {
						return candidate, true
					}
					start = end
					continue scan
				}
			}
		}
	}
	return nil, false
}

func ReadKeyManifest(path string) (KeyManifest, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return KeyManifest{}, fmt.Errorf("read key manifest: %w", err)
	}
	manifest, err := readKeyManifestBytes(bytes)
	if err != nil {
		return KeyManifest{}, err
	}
	return manifest, nil
}

func readKeyManifestBytes(bytes []byte) (KeyManifest, error) {
	var raw map[string]any
	if err := json.Unmarshal(bytes, &raw); err != nil {
		return KeyManifest{}, fmt.Errorf("parse key manifest: %w", err)
	}
	var defaultKey string
	keys := map[string]string{}
	for key, value := range raw {
		if key == "__default_key" || key == "default_key" {
			normalized, err := normalizeManifestKey("__default_key", value)
			if err != nil {
				return KeyManifest{}, err
			}
			defaultKey = normalized
			continue
		}
		if key == "keys" {
			nested, ok := value.(map[string]any)
			if !ok {
				return KeyManifest{}, errors.New("keys must be an object")
			}
			if err := readManifestKeyMap(keys, nested); err != nil {
				return KeyManifest{}, err
			}
			continue
		}
		if strings.HasPrefix(key, "__") {
			continue
		}
		if err := readManifestKeyMap(keys, map[string]any{key: value}); err != nil {
			return KeyManifest{}, err
		}
	}
	if len(keys) == 0 && defaultKey == "" {
		return KeyManifest{}, errors.New("key manifest did not contain database keys or default key")
	}
	return KeyManifest{Keys: keys, DefaultKey: defaultKey}, nil
}

func readManifestKeyMap(keys map[string]string, raw map[string]any) error {
	for key, value := range raw {
		rel, err := cleanManifestKeyPath(key)
		if err != nil {
			return err
		}
		normalized, err := normalizeManifestKey(key, value)
		if err != nil {
			return err
		}
		keys[rel] = normalized
	}
	return nil
}

func cleanManifestKeyPath(path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid database path %q", path)
	}
	return clean, nil
}

func normalizeManifestKey(name string, value any) (string, error) {
	stringValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid key for %s", name)
	}
	stringValue = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(stringValue), "x'"), "0x")
	stringValue = strings.TrimSuffix(stringValue, "'")
	if !hexKeyRE.MatchString(stringValue) {
		return "", fmt.Errorf("invalid key for %s", name)
	}
	return strings.ToLower(stringValue), nil
}

func cleanManifestRel(rel string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(rel))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid database path %q", rel)
	}
	return clean, nil
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
	resolved, err := resolveSnapshotKeys(dbRoot, keys)
	if err != nil {
		return result, err
	}
	relPaths := make([]string, 0, len(resolved))
	for rel := range resolved {
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
		if err := decryptOne(ctx, sqlcipher, src, dst, resolved[rel]); err != nil {
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
	return checkSnapshotKeys(context.Background(), opts, false)
}

func ProbeSnapshotKeys(ctx context.Context, opts DecryptOptions) (KeyReadiness, error) {
	return checkSnapshotKeys(ctx, opts, true)
}

func checkSnapshotKeys(ctx context.Context, opts DecryptOptions, probe bool) (KeyReadiness, error) {
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
	resolved, err := resolveSnapshotKeys(dbRoot, keys)
	if err != nil {
		return result, err
	}
	result.KeyCount = len(resolved)
	result.DefaultKey = keys.DefaultKey != ""
	relPaths := make([]string, 0, len(resolved))
	for rel := range resolved {
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
		if probe {
			if err := probeOne(ctx, sqlcipher, src, resolved[rel]); err != nil {
				result.ProbeFailed = append(result.ProbeFailed, DecryptEntry{Database: rel, Reason: err.Error()})
				continue
			}
			result.Probed = append(result.Probed, DecryptEntry{Database: rel})
		}
	}
	result.Ready = len(result.Found) > 0 && len(result.Missing) == 0
	if probe {
		result.ProbeReady = result.Ready && len(result.Probed) == len(result.Found) && len(result.ProbeFailed) == 0
	}
	return result, nil
}

func resolveSnapshotKeys(dbRoot string, manifest KeyManifest) (map[string]string, error) {
	keys := map[string]string{}
	for path, key := range manifest.Keys {
		rel, err := resolveManifestDBPath(dbRoot, path)
		if err != nil {
			return nil, err
		}
		keys[rel] = key
	}
	if manifest.DefaultKey == "" {
		return keys, nil
	}
	err := filepath.WalkDir(dbRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			return nil
		}
		rel, err := filepath.Rel(dbRoot, path)
		if err != nil {
			return err
		}
		rel, err = cleanManifestRel(rel)
		if err != nil {
			return err
		}
		if _, ok := keys[rel]; !ok {
			keys[rel] = manifest.DefaultKey
		}
		return nil
	})
	return keys, err
}

func resolveManifestDBPath(dbRoot, path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." {
		return "", fmt.Errorf("invalid database path %q", path)
	}
	if rel, ok := relAfterDBStorage(clean); ok {
		return cleanManifestRel(rel)
	}
	if !filepath.IsAbs(clean) {
		return cleanManifestRel(clean)
	}
	dbRootAbs, err := filepath.Abs(dbRoot)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(dbRootAbs, clean); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return cleanManifestRel(rel)
	}
	return "", fmt.Errorf("absolute database path is not under copied db_storage: %s", clean)
}

func relAfterDBStorage(path string) (string, bool) {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "db_storage" && i+1 < len(parts) {
			return filepath.Join(parts[i+1:]...), true
		}
	}
	return "", false
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

func probeOne(ctx context.Context, sqlcipher, src, keyHex string) error {
	commands := fmt.Sprintf(`PRAGMA key = "x'%s'";
PRAGMA cipher_page_size = 4096;
PRAGMA quick_check;
`, keyHex)
	cmd := exec.CommandContext(ctx, sqlcipher, src)
	cmd.Stdin = strings.NewReader(commands)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return fmt.Errorf("sqlcipher probe failed: %s", text)
	}
	if !allOKLines(text) {
		return fmt.Errorf("sqlcipher probe returned %q", text)
	}
	return nil
}

func allOKLines(text string) bool {
	lines := strings.Fields(strings.TrimSpace(text))
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		if line != "ok" {
			return false
		}
	}
	return true
}
