package core

import (
	"testing"
	"time"

	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
)

// TestActivePackResolution pins the character resolution order the
// rover's walk speed depends on: the preferred pack while it is
// registered, the builtin, then whatever the registry has, so both
// sides of the app resolve the same character.
func TestActivePackResolution(t *testing.T) {
	c := NewCore(t.TempDir(), t.TempDir(), "")

	// A fresh store only carries the builtin.
	if got := c.ActivePack().ID; got != petfeed.BuiltinAssistantPackID {
		t.Fatalf("fresh store resolves %q, want the builtin", got)
	}

	// The preference wins while the character is installed.
	c.Packs.Register(petfeed.Pack{ID: "neko"})
	if err := c.Shell.SetAssistantPetCharacter("neko"); err != nil {
		t.Fatalf("SetAssistantPetCharacter: %v", err)
	}
	if got := c.ActivePack().ID; got != "neko" {
		t.Fatalf("preferred pack resolves %q, want neko", got)
	}

	// Uninstalling it must not leave the pet undrivable.
	c.Packs.Unregister("neko")
	if got := c.ActivePack().ID; got != petfeed.BuiltinAssistantPackID {
		t.Fatalf("after uninstall resolves %q, want the builtin", got)
	}

	// A registry without the builtin still renders something.
	c.Packs = petfeed.NewPackStore(petfeed.Pack{ID: "only"})
	if got := c.ActivePack().ID; got != "only" {
		t.Fatalf("builtin-less store resolves %q, want the only pack", got)
	}
}

// TestShellPetRuntimeStatusRoundTrip covers the mount report the
// diagnostics panel reads back, including the teardown that has to stop
// it from describing a window that is gone.
func TestShellPetRuntimeStatusRoundTrip(t *testing.T) {
	s := NewShell(t.TempDir())

	if _, reported := s.PetRuntimeStatus(); reported {
		t.Fatal("a fresh shell must not report a mount")
	}

	s.SetPetRuntimeStatus(petfeed.RuntimeStatus{
		PackID:   "assistant-default",
		Artboard: "Pet",
		OK:       true,
	})
	status, reported := s.PetRuntimeStatus()
	if !reported {
		t.Fatal("reported must be true after a report")
	}
	if status.PackID != "assistant-default" || !status.OK {
		t.Fatalf("stored status = %+v", status)
	}
	if status.ReportedAt.IsZero() ||
		time.Since(status.ReportedAt) > time.Minute {
		t.Fatalf("ReportedAt was not stamped: %v", status.ReportedAt)
	}

	s.ClearPetRuntimeStatus()
	if _, reported := s.PetRuntimeStatus(); reported {
		t.Fatal("reported must be false after teardown")
	}
}
