package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/profile"
)

// firstRunPrefsMode is the default mode written into fresh preference
// documents in the current build profile.
func firstRunPrefsMode() string {
	if profile.YoloOnly() {
		return "yolo"
	}
	return "workspace"
}

func TestShellPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewShell(dir)
	if !s.GetCloseToTray() {
		t.Fatal("default close behavior should hide to tray")
	}
	if err := s.SetCloseToTray(false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}

	reloaded := NewShell(dir)
	if reloaded.GetCloseToTray() {
		t.Fatal("closeToTray=false did not persist")
	}
	if got := reloaded.Language(); got != "zh" {
		t.Fatalf("language = %q, want zh", got)
	}
}

func TestShellQuitState(t *testing.T) {
	s := NewShell(t.TempDir())
	s.MarkQuitting()
	s.mu.Lock()
	quitting := s.quitting
	confirmed := s.quitConfirmed
	s.mu.Unlock()
	if !quitting || confirmed {
		t.Fatalf("quit state after MarkQuitting = (%v,%v)", quitting, confirmed)
	}
	s.clearQuitRequest()
	s.mu.Lock()
	quitting = s.quitting
	confirmed = s.quitConfirmed
	s.mu.Unlock()
	if quitting || confirmed {
		t.Fatalf("quit state after clear = (%v,%v)", quitting, confirmed)
	}
}

func TestShellContextFallsBackBeforeStartup(t *testing.T) {
	s := NewShell(t.TempDir())
	if s.Context() == nil {
		t.Fatal("Context must never be nil before Startup")
	}
	type shellContextKey struct{}
	ctx := context.WithValue(context.Background(), shellContextKey{}, "wails")
	s.SetContext(ctx)
	if s.Context() != ctx {
		t.Fatal("Context must return the installed Wails context")
	}
}

func TestShellSessionDefaultsPersist(t *testing.T) {
	dir := t.TempDir()
	s := NewShell(dir)
	if mode, think := s.SessionDefaults(); mode != firstRunPrefsMode() || think != "medium" {
		t.Fatalf("defaults = (%q, %q)", mode, think)
	}
	persistMode := "read-only"
	if profile.YoloOnly() {
		// The yoloonly build only stores yolo defaults; confined
		// modes are repaired on load and rejected by the bindings.
		persistMode = "yolo"
	}
	if err := s.SetSessionDefaults(persistMode, "high"); err != nil {
		t.Fatal(err)
	}
	// Other setters must not wipe the new fields when they rewrite
	// the preference document.
	if err := s.SetCloseToTray(false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLanguage("zh"); err != nil {
		t.Fatal(err)
	}

	reloaded := NewShell(dir)
	if mode, think := reloaded.SessionDefaults(); mode != persistMode || think != "high" {
		t.Fatalf("defaults after reload = (%q, %q)", mode, think)
	}
}

func TestLoadPrefsOldFileFallsBackToSessionDefaults(t *testing.T) {
	dir := t.TempDir()
	data := `{"closeToTray":false,"language":"zh"}`
	if err := os.WriteFile(
		filepath.Join(dir, prefsFile), []byte(data), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	prefs := LoadPrefs(dir)
	if prefs.DefaultMode != firstRunPrefsMode() || prefs.DefaultThink != "medium" {
		t.Fatalf("defaults = (%q, %q)", prefs.DefaultMode, prefs.DefaultThink)
	}
	if prefs.CloseToTray {
		t.Fatal("stored closeToTray=false did not load")
	}
}

func TestLoadPrefsInvalidSessionDefaultsFallBack(t *testing.T) {
	dir := t.TempDir()
	data := `{
		"closeToTray": false,
		"defaultMode": "dangerous",
		"defaultThink": "ultra"
	}`
	if err := os.WriteFile(
		filepath.Join(dir, prefsFile), []byte(data), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	prefs := LoadPrefs(dir)
	if prefs.DefaultMode != firstRunPrefsMode() || prefs.DefaultThink != "medium" {
		t.Fatalf(
			"invalid values were not repaired: (%q, %q)",
			prefs.DefaultMode, prefs.DefaultThink,
		)
	}
	if prefs.CloseToTray {
		t.Fatal("stored closeToTray=false did not load")
	}
}
