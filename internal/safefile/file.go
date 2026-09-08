package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve includes existing parent symlinks even when the final path is new.
func Resolve(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Abs(resolved)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// Split preserves symlink/.. traversal; Dir and Abs clean it too early.
	parent, base := filepath.Split(path)
	if base == "" {
		trimmed := strings.TrimRight(path, string(os.PathSeparator))
		if trimmed == "" || trimmed == path {
			return "", err
		}
		return Resolve(trimmed)
	}
	if parent == "" {
		parent = "."
	}
	if parent == path {
		return "", err
	}
	resolved, err = Resolve(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, base), nil
}

func RejectAliases(path string, protected ...string) error {
	resolved, err := Resolve(path)
	if err != nil {
		return err
	}
	info, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	for _, input := range protected {
		other, err := Resolve(input)
		if err != nil {
			return err
		}
		otherInfo, _ := os.Stat(input)
		if resolved == other || (info != nil && otherInfo != nil && os.SameFile(info, otherInfo)) {
			return fmt.Errorf("output aliases protected input %s", input)
		}
		if strings.EqualFold(resolved, other) {
			folded, err := caseInsensitiveParent(resolved)
			if err != nil {
				return err
			}
			if folded {
				return fmt.Errorf("output aliases protected input %s", input)
			}
		}
	}
	return nil
}

// Probe the destination volume without creating either candidate pathname.
func caseInsensitiveParent(path string) (folded bool, resultErr error) {
	dir := filepath.Dir(path)
	for {
		_, err := os.Stat(dir)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(dir) == dir {
			return false, err
		}
		dir = filepath.Dir(dir)
	}
	file, err := os.CreateTemp(dir, ".weicrawl-case-*")
	if err != nil {
		return false, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close(), os.Remove(file.Name()))
	}()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	alias := filepath.Join(dir, strings.ToUpper(filepath.Base(file.Name())))
	other, err := os.Stat(alias)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(info, other), nil
}

func RejectOverlap(a, b string) error {
	left, err := Resolve(a)
	if err != nil {
		return err
	}
	right, err := Resolve(b)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return err
		}
		if filepath.IsLocal(rel) || rel == "." {
			return fmt.Errorf("source and output directories overlap")
		}
		info, err := os.Stat(pair[0])
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info == nil {
			continue
		}
		// Filesystem identity also covers case-folded ancestor spellings.
		for ancestor := pair[1]; ; ancestor = filepath.Dir(ancestor) {
			other, err := os.Stat(ancestor)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if other != nil && os.SameFile(info, other) {
				return fmt.Errorf("source and output directories overlap")
			}
			if ancestor == filepath.Dir(ancestor) {
				break
			}
		}
	}
	return nil
}

// Write replaces only a completed private output; failure preserves the old file.
func Write(path string, write func(*os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".weicrawl-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := write(file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func WriteBytes(path string, data []byte) error {
	return Write(path, func(file *os.File) error {
		_, err := file.Write(data)
		return err
	})
}
