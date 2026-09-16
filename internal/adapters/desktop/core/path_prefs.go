package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathPrefs is the persisted PATH override. The precedence rules and the
// startup install live in foundation/utils/envpath; this document only
// stores what the user configured.
type PathPrefs struct {
	// Prepend lists absolute directories placed in front of the process
	// PATH. It is the escape hatch for installs the candidate table
	// cannot know about, such as a version manager's active directory.
	Prepend []string `json:"prepend,omitempty"`
}

// normalizePathPrefs repairs a PATH override that came from disk or from a
// caller: entries are trimmed, blanks and repeats are dropped, and the order
// is preserved. Relative entries are kept on purpose so the diagnostics view
// can report them as rejected instead of silently forgetting what the user
// typed; the install step is what refuses to apply them.
func normalizePathPrefs(prefs PathPrefs) PathPrefs {
	seen := make(map[string]bool, len(prefs.Prepend))
	prepend := make([]string, 0, len(prefs.Prepend))
	for _, dir := range prefs.Prepend {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		prepend = append(prepend, dir)
	}
	prefs.Prepend = prepend
	return prefs
}

// validatePathPrefs rejects the values the renderer must never send. Only
// absolute directories are accepted: a relative entry would resolve against
// whatever working directory the app happens to spawn from, which is both
// surprising and a hazard for a desktop app that runs in the user's
// workspace.
func validatePathPrefs(prefs PathPrefs) error {
	for _, dir := range prefs.Prepend {
		if !filepath.IsAbs(strings.TrimSpace(dir)) {
			return fmt.Errorf("path prefs: %q is not an absolute directory", dir)
		}
	}
	return nil
}
