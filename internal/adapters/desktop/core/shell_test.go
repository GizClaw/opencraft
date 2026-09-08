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
	if s.QuitRequested() {
		t.Fatal("fresh shell must not report a quit flow")
	}
	s.MarkQuitting()
	s.mu.Lock()
	quitting := s.quitting
	confirmed := s.quitConfirmed
	s.mu.Unlock()
	if !quitting || confirmed {
		t.Fatalf("quit state after MarkQuitting = (%v,%v)", quitting, confirmed)
	}
	if !s.QuitRequested() {
		t.Fatal("MarkQuitting must be visible through QuitRequested")
	}
	s.clearQuitRequest()
	s.mu.Lock()
	quitting = s.quitting
	confirmed = s.quitConfirmed
	s.mu.Unlock()
	if quitting || confirmed {
		t.Fatalf("quit state after clear = (%v,%v)", quitting, confirmed)
	}
	if s.QuitRequested() {
		t.Fatal("clearQuitRequest must also reset QuitRequested")
	}
}

func TestShellLanguageChangedListener(t *testing.T) {
	s := NewShell(t.TempDir())
	var notified []string
	s.SetLanguageChangedListener(func() {
		notified = append(notified, s.Language())
	})
	if err := s.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 || notified[0] != "zh" {
		t.Fatalf("language listener notified with %v, want [zh]", notified)
	}
}

func TestShellShouldQuitWithoutConfirmation(t *testing.T) {
	s := NewShell(t.TempDir())
	s.SetScheduledTasksChecker(func(context.Context) bool { return false })
	if !s.ShouldQuit() {
		t.Fatal("quit with no scheduled tasks must proceed immediately")
	}
	if !s.quitting || !s.quitConfirmed {
		t.Fatalf("quit state after ShouldQuit = (%v,%v)",
			s.quitting, s.quitConfirmed)
	}
}

func TestShellShouldQuitConfirmedRepeats(t *testing.T) {
	s := NewShell(t.TempDir())
	s.SetScheduledTasksChecker(func(context.Context) bool { return false })
	if !s.ShouldQuit() {
		t.Fatal("first ShouldQuit should confirm")
	}
	if !s.ShouldQuit() {
		t.Fatal("confirmed quit must stay allowed on repeated calls")
	}
	s.clearQuitRequest()
	if s.quitting || s.quitConfirmed {
		t.Fatal("clearQuitRequest must reset both flags")
	}
}

func TestShellShouldQuitIgnoresRepeatedRequestWhileDialogPending(t *testing.T) {
	s := NewShell(t.TempDir())
	s.MarkQuitting()
	if s.ShouldQuit() {
		t.Fatal("pending unconfirmed quit must not open a second dialog")
	}
	if !s.quitting || s.quitConfirmed {
		t.Fatalf("pending state changed to (%v,%v)",
			s.quitting, s.quitConfirmed)
	}
}

func TestShellConfirmQuitRequired(t *testing.T) {
	s := NewShell(t.TempDir())
	if !s.confirmQuitRequired(context.Background()) {
		t.Fatal("unwired shell must keep the historic always-confirm behavior")
	}

	s.SetScheduledTasksChecker(func(context.Context) bool { return false })
	if s.confirmQuitRequired(context.Background()) {
		t.Fatal("no scheduled tasks should skip the quit dialog")
	}

	s.SetScheduledTasksChecker(func(context.Context) bool { return true })
	if !s.confirmQuitRequired(context.Background()) {
		t.Fatal("scheduled tasks present should keep the quit dialog")
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
