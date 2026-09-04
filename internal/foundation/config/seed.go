package config

import (
	"os"
	"path/filepath"
)

// EnsureUserConfig ensures the user configuration directory and the
// sandbox cache exist and returns the config directory. Configuration
// documents are NOT seeded here anymore: the settings page
// writes the user layer (~/.opencraft/config/opencraft.yaml) so every
// user-visible setting lives in that single editable document. The
// default graph and its node sources also stay embedded unless a
// config layer overrides the graph reference.
func EnsureUserConfig() (string, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return "", err
	}
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
