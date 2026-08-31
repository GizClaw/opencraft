package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrefsDefaultCloseToTray(t *testing.T) {
	dir := t.TempDir()
	prefs, err := loadPrefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.CloseToTray {
		t.Fatal("close-to-tray should default to true")
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := savePrefs(dir, desktopPrefs{CloseToTray: false}); err != nil {
		t.Fatal(err)
	}
	prefs, err := loadPrefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.CloseToTray {
		t.Fatal("close-to-tray should persist as false")
	}
	// A second save overwrites atomically.
	if err := savePrefs(dir, desktopPrefs{CloseToTray: true}); err != nil {
		t.Fatal(err)
	}
	prefs, err = loadPrefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.CloseToTray {
		t.Fatal("close-to-tray should persist as true after rewrite")
	}
}

func TestPrefsCorruptFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := loadPrefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.CloseToTray {
		t.Fatal("corrupt prefs should fall back to defaults")
	}
}

func TestSetCloseToTrayPersists(t *testing.T) {
	dir := t.TempDir()
	a := &App{userDir: dir, closeToTray: true}
	if err := a.SetCloseToTray(false); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetCloseToTray()
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("SetCloseToTray(false) should be visible to GetCloseToTray")
	}
	// Reload from disk: the value must survive a restart.
	prefs, err := loadPrefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.CloseToTray {
		t.Fatal("persisted close-to-tray should be false")
	}
}
