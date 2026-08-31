package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectConfigDir finds the nearest .opencraft/config directory above
// workDir that actually contains a user-overridable document
// (opencraft.yaml). The global ~/.opencraft/config is
// excluded so discovery never mistakes it for a project layer.
func ProjectConfigDir(workDir string) (string, bool) {
	if strings.TrimSpace(workDir) == "" {
		// Empty means "no workspace": never walk the current directory
		// (Finder-launched apps have cwd "/") looking for a project
		// layer.
		return "", false
	}
	userDir, _ := UserConfigDir()
	dir := workDir
	for {
		candidate := filepath.Clean(filepath.Join(dir, ".opencraft", "config"))
		if candidate != filepath.Clean(userDir) {
			if st, err := os.Stat(candidate); err == nil && st.IsDir() {
				for _, name := range []string{"opencraft.yaml"} {
					if _, err := os.Stat(filepath.Join(candidate, name)); err == nil {
						return candidate, true
					}
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}
