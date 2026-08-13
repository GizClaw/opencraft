package config

import (
	"errors"
	"os"
	"path/filepath"
)

// EnsureUserConfig seeds ~/.opencraft/config with the user-facing assets
// and returns the directory. Existing files are preserved (user edits
// win); deleted files are regenerated on the next start.
func EnsureUserConfig() (string, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return "", err
	}
	for _, asset := range UserAssets {
		target := filepath.Join(dir, filepath.FromSlash(asset.Name))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		data, err := FS().ReadFile(asset.Ref)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return "", err
		}
	}
	// The sandbox cache directory (~/.opencraft/cache) is created at
	// assembly time by the app; there is no user-editable sandbox
	// document anymore — the sandbox backend is selected from the
	// platform in internal/app.
	dataDir, err := UserDataDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(dataDir, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}
