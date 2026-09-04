package core

import (
	"context"
	"testing"
)

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
