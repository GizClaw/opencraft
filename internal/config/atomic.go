package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a temp file in the same
// directory followed by rename, so a crash mid-write never leaves a
// truncated user configuration file. The final file inherits perm
// (0600 for the secrets-bearing opencraft.yaml).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".opencraft-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// No-op after a successful rename (the name no longer exists).
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: rename temp file: %w", err)
	}
	return nil
}
