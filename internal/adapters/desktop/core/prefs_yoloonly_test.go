//go:build yoloonly

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestYoloOnlyPrefsAndConversationDefaults(t *testing.T) {
	prefs := DefaultPrefs()
	if prefs.DefaultMode != "yolo" {
		t.Fatalf("DefaultPrefs mode = %q, want yolo", prefs.DefaultMode)
	}

	dir := t.TempDir()
	data := `{"closeToTray":false,"defaultMode":"workspace","defaultThink":"high"}`
	if err := os.WriteFile(
		filepath.Join(dir, prefsFile), []byte(data), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	loaded := LoadPrefs(dir)
	if loaded.DefaultMode != "yolo" {
		t.Fatalf("normalized mode = %q, want yolo", loaded.DefaultMode)
	}

	c := NewConversation()
	c.New("/tmp/w")
	if got := c.Mode("/tmp/w"); got != "yolo" {
		t.Fatalf("minted conversation mode = %q, want yolo", got)
	}
}
