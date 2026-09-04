package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

const prefsFile = "desktop.json"

// DesktopPrefs is the persisted desktop preference document.
type DesktopPrefs struct {
	CloseToTray bool   `json:"closeToTray"`
	Language    string `json:"language,omitempty"`
}

// LoadPrefs reads the desktop preference file with defaults.
func LoadPrefs(userDir string) DesktopPrefs {
	prefs := DesktopPrefs{CloseToTray: true}
	if userDir == "" {
		if dir, err := config.UserConfigDir(); err == nil {
			userDir = dir
		}
	}
	data, err := os.ReadFile(filepath.Join(userDir, prefsFile))
	if errors.Is(err, os.ErrNotExist) {
		return prefs
	}
	if err != nil {
		return prefs
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		return prefs
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return prefs
	}
	_ = json.Unmarshal(raw, &prefs)
	return prefs
}

// SavePrefs atomically writes the preference file.
func SavePrefs(userDir string, prefs DesktopPrefs) error {
	if userDir == "" {
		var err error
		userDir, err = config.UserConfigDir()
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(userDir, prefsFile)
	tmp, err := os.CreateTemp(userDir, ".desktop-*.tmp")
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
