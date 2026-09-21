package config

import (
	"os"
	"path/filepath"
	"strings"
)

// EnsureUserConfig ensures the user configuration directory and the
// sandbox cache exist and returns the config directory. appHome is the
// shared content root (config/, keyring/, plugins/) and dataRoot the
// state root (cache/, workspaces/, user.db); an empty appHome falls back
// to the state root and an empty dataRoot to the app home, which keeps
// the historical single-root callers working. Configuration documents
// are NOT seeded here anymore: the settings page
// writes the user layer (~/.opencraft/config/opencraft.yaml) so every
// user-visible setting lives in that single editable document. The
// default graph and its node sources also stay embedded unless a
// config layer overrides the graph reference.
func EnsureUserConfig(appHome, dataRoot string) (string, error) {
	if strings.TrimSpace(appHome) == "" {
		var err error
		if appHome, err = UserDataDir(); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = appHome
	}
	dir := filepath.Join(appHome, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	cacheDir := filepath.Join(dataRoot, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}
