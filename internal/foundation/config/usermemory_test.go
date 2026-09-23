package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUserMemoryResolveDefaultsAndBounds pins the shape the settings page
// and the runtime share: a nil enabled means on, a zero budget means the
// shipped default (never "inject nothing"), and an out-of-range value is
// refused rather than clamped.
func TestUserMemoryResolveDefaultsAndBounds(t *testing.T) {
	defaults := DefaultUserMemorySettings()
	if defaults.Enabled == nil || !*defaults.Enabled {
		t.Fatalf("shipped enabled = %v, want on by default", defaults.Enabled)
	}
	if defaults.InjectMaxItems != UserMemoryDefaultInjectMaxItems ||
		defaults.InjectMaxChars != UserMemoryDefaultInjectMaxChars {
		t.Fatalf("shipped budgets = %+v", defaults)
	}

	resolved, err := UserMemorySettings{}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Enabled {
		t.Fatal("a nil enabled must resolve to on")
	}
	if resolved.InjectMaxItems != UserMemoryDefaultInjectMaxItems ||
		resolved.InjectMaxChars != UserMemoryDefaultInjectMaxChars {
		t.Fatalf("resolved = %+v, want the defaults", resolved)
	}

	off, err := UserMemorySettings{Enabled: boolPtr(false)}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled {
		t.Fatal("an explicit false must survive Resolve")
	}

	for _, bad := range []UserMemorySettings{
		{InjectMaxItems: -1},
		{InjectMaxItems: UserMemoryMaxInjectMaxItems + 1},
		{InjectMaxChars: UserMemoryMinInjectMaxChars - 1},
		{InjectMaxChars: UserMemoryMaxInjectMaxChars + 1},
	} {
		if _, err := bad.Resolve(); err == nil {
			t.Fatalf("Resolve(%+v) accepted an out-of-range budget", bad)
		}
	}

	if _, err := (UserMemorySettings{
		InjectMaxItems: UserMemoryMaxInjectMaxItems,
		InjectMaxChars: UserMemoryMaxInjectMaxChars,
	}).Resolve(); err != nil {
		t.Fatalf("the bounds themselves must be accepted: %v", err)
	}
}

// TestUserMemoryUserLayerRoundTrip pins Load -> Save -> Load: before a
// save the effective settings are the embedded defaults, a save is
// readable back verbatim, and a refused save leaves the stored layer
// alone.
func TestUserMemoryUserLayerRoundTrip(t *testing.T) {
	dir := t.TempDir()

	embedded, err := LoadUserMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.Enabled == nil || !*embedded.Enabled ||
		embedded.InjectMaxItems != UserMemoryDefaultInjectMaxItems ||
		embedded.InjectMaxChars != UserMemoryDefaultInjectMaxChars {
		t.Fatalf("embedded defaults = %+v", embedded)
	}

	saved := UserMemorySettings{
		Enabled:        boolPtr(false),
		InjectMaxItems: 20,
		InjectMaxChars: 4096,
	}
	if err := SaveUserMemory(dir, saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadUserMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Enabled == nil || *loaded.Enabled {
		t.Fatalf("enabled = %v, want the saved false", loaded.Enabled)
	}
	if loaded.InjectMaxItems != 20 || loaded.InjectMaxChars != 4096 {
		t.Fatalf("loaded = %+v, want the saved budgets", loaded)
	}

	if err := SaveUserMemory(dir, UserMemorySettings{InjectMaxItems: 999}); err == nil {
		t.Fatal("SaveUserMemory must refuse out-of-range settings")
	}
	again, err := LoadUserMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.InjectMaxItems != 20 {
		t.Fatalf("a refused save corrupted the layer: %+v", again)
	}

	info, err := os.Stat(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("user layer mode = %o, want 0600", perm)
	}
}
