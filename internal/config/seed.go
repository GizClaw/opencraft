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
	for _, ref := range UserAssets {
		target := filepath.Join(dir, filepath.FromSlash(ref))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		data, err := FS().ReadFile("assets/" + ref)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return "", err
		}
	}
	// sandbox.yaml is platform-specific (build-tag selected embed), so it
	// renders from the platform template with the cache directory
	// (~/.opencraft/cache) expanded to an absolute path.
	sandboxTarget := filepath.Join(dir, "sandbox.yaml")
	if _, err := os.Stat(sandboxTarget); errors.Is(err, os.ErrNotExist) {
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
		if err := os.WriteFile(
			sandboxTarget, SandboxYAML(cacheDir), 0o600); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return dir, nil
}
