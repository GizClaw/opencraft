package plugins

import (
	"path/filepath"
	"testing"
)

// TestStoreRevisionMovesOnMutations pins the register clock: every
// successful registry mutation moves the revision exactly once, reads
// never move it, and a mutation that changes nothing (enabling an
// already-enabled plugin) does not move it — the desktop refresh and
// the agent host both key off this number (see charter.go's
// FaceRefreshes).
func TestStoreRevisionMovesOnMutations(t *testing.T) {
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "old-bundle")
	newSrc := t.TempDir()
	writePlugin(t, newSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "new-bundle")

	s := NewStore(t.TempDir())
	rev := func() uint64 { return s.Revision() }
	if got := rev(); got != 0 {
		t.Fatalf("revision = %d, want 0 for a fresh store", got)
	}

	// Reads never move it.
	if _, err := s.List(); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := s.Inspect(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got := rev(); got != 0 {
		t.Fatalf("revision after reads = %d, want 0", got)
	}

	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := rev(); got != 1 {
		t.Fatalf("revision after install = %d, want 1", got)
	}

	// Enabled is the default, so this changes nothing.
	if err := s.SetEnabled("p", true); err != nil {
		t.Fatalf("no-op enable: %v", err)
	}
	if got := rev(); got != 1 {
		t.Fatalf("revision after no-op enable = %d, want 1", got)
	}

	if err := s.SetEnabled("p", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := rev(); got != 2 {
		t.Fatalf("revision after disable = %d, want 2", got)
	}
	if err := s.SetEnabled("p", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := rev(); got != 3 {
		t.Fatalf("revision after enable = %d, want 3", got)
	}

	if _, err := s.Update("p", filepath.Join(newSrc, "p")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := rev(); got != 4 {
		t.Fatalf("revision after update = %d, want 4", got)
	}
	if _, err := s.Rollback("p"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := rev(); got != 5 {
		t.Fatalf("revision after rollback = %d, want 5", got)
	}
	if err := s.Uninstall("p"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if got := rev(); got != 6 {
		t.Fatalf("revision after uninstall = %d, want 6", got)
	}

	// A failed mutation changes nothing and moves nothing.
	if err := s.SetEnabled("p", false); err == nil {
		t.Fatal("SetEnabled on an uninstalled plugin must fail")
	}
	if got := rev(); got != 6 {
		t.Fatalf("revision after failed mutation = %d, want 6", got)
	}
}
