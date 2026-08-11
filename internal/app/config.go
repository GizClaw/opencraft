package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/GizClaw/opencraft/internal/config"
)

// EnsureUserConfig seeds ~/.opencraft/config with the user-facing assets
// and returns the directory. Existing files are preserved (user edits
// win); deleted files are regenerated on the next start.
func EnsureUserConfig() (string, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return "", err
	}
	for _, ref := range config.UserAssets {
		target := filepath.Join(dir, filepath.FromSlash(ref))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		data, err := config.FS().ReadFile(ref)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}
