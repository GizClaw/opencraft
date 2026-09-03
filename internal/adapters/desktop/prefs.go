package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// prefsFile is the user-facing desktop preference file. It lives next to
// the other user configuration under ~/.opencraft/config so preferences
// survive app restarts and are included in any config backup.
const prefsFile = "desktop.json"

// desktopPrefs is the persisted desktop preference document. Add fields
// with json tags; unknown fields in an existing file are preserved by
// loading into a map first (see loadPrefs).
type desktopPrefs struct {
	// CloseToTray hides the app/window on close instead of quitting.
	// On macOS the close button hides the whole app (the default
	// macOS behaviour); on Windows/Linux the window is hidden and the
	// app keeps running in the system tray. When false, closing quits.
	CloseToTray bool `json:"closeToTray"`
	// Language is the desktop UI language ("zh" or "en"). Empty means
	// the process locale decides until the frontend syncs its choice.
	Language string `json:"language,omitempty"`
}

// loadPrefs reads the desktop preference file. A missing or corrupt file
// yields the defaults; only a genuinely unreadable file returns an error
// (the caller treats it as best-effort).
func loadPrefs(userDir string) (desktopPrefs, error) {
	prefs := desktopPrefs{CloseToTray: true}
	dir := userDir
	if dir == "" {
		var err error
		dir, err = config.UserConfigDir()
		if err != nil {
			return prefs, nil
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, prefsFile))
	if errors.Is(err, os.ErrNotExist) {
		return prefs, nil
	}
	if err != nil {
		return prefs, err
	}
	// Start from defaults, then overlay whatever keys the file has so a
	// future version's new fields keep their defaults when absent.
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		return prefs, nil
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return prefs, nil
	}
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return prefs, nil
	}
	return prefs, nil
}

// savePrefs atomically writes the preference file (temp file + rename so
// a crash mid-write never leaves a truncated document).
func savePrefs(userDir string, prefs desktopPrefs) error {
	dir := userDir
	if dir == "" {
		var err error
		dir, err = config.UserConfigDir()
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, prefsFile)
	tmp, err := os.CreateTemp(dir, ".desktop-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
