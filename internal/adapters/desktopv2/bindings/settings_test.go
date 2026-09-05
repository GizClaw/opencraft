package bindings

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
)

func TestSettingsSessionDefaultsRoundTrip(t *testing.T) {
	userDir := t.TempDir()
	b := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))

	got := b.GetSessionDefaults()
	if got.Mode != "workspace" || got.Think != "medium" {
		t.Fatalf("first-run defaults = (%q, %q)", got.Mode, got.Think)
	}
	if err := b.SetSessionDefaults(SessionDefaults{
		Mode: "read-only", Think: "high",
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))
	got = reloaded.GetSessionDefaults()
	if got.Mode != "read-only" || got.Think != "high" {
		t.Fatalf("defaults after reload = (%q, %q)", got.Mode, got.Think)
	}
}

func TestSettingsSessionDefaultsRejectsInvalidValues(t *testing.T) {
	userDir := t.TempDir()
	b := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))

	if err := b.SetSessionDefaults(SessionDefaults{
		Mode: "unrestricted", Think: "medium",
	}); err == nil {
		t.Fatal("unknown mode was accepted")
	}
	if err := b.SetSessionDefaults(SessionDefaults{
		Mode: "workspace", Think: "ultra",
	}); err == nil {
		t.Fatal("unknown think level was accepted")
	}

	got := b.GetSessionDefaults()
	if got.Mode != "workspace" || got.Think != "medium" {
		t.Fatalf(
			"rejected write changed defaults to (%q, %q)",
			got.Mode, got.Think,
		)
	}
}
