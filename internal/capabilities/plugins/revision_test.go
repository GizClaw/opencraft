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

// TestStoreRevisionIgnoresRefusedMutations is the other half of the same
// clock: a mutation the registry refuses before it touches anything —
// a re-install of an installed id, an update that is not newer, a
// rollback with no snapshot, uninstalling a builtin — leaves the
// revision where it was, so no face rebuilds for it. The zip entry
// points move it exactly once, like their directory twins.
func TestStoreRevisionIgnoresRefusedMutations(t *testing.T) {
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", testManifest("p", "0.1.0"), "old")
	sameSrc := t.TempDir()
	writePlugin(t, sameSrc, "p", testManifest("p", "0.1.0"), "same")
	newSrc := t.TempDir()
	writePlugin(t, newSrc, "p", testManifest("p", "0.2.0"), "new")
	newZip := writeTestZip(t, map[string]string{
		"p/plugin.json":   `{"id":"p","name":"p","version":"0.3.0","entry":"dist/index.js"}`,
		"p/dist/index.js": "bundle",
	})
	freshZip := writeTestZip(t, map[string]string{
		"zip-plugin/plugin.json":   `{"id":"zip-plugin","name":"Zip","version":"1.0.0","entry":"dist/index.js"}`,
		"zip-plugin/dist/index.js": "bundle",
	})
	builtin := builtinDir(t)
	writeBuiltinPlugin(t, builtin, "bundled", testManifest("bundled", "1.0.0"), "bundle", "")

	s := NewStore(t.TempDir())
	rev := func() uint64 { return s.Revision() }
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	base := rev()

	if _, err := s.Install(filepath.Join(oldSrc, "p")); err == nil {
		t.Fatal("re-installing an installed id must fail")
	}
	if got := rev(); got != base {
		t.Fatalf("revision after refused re-install = %d, want %d", got, base)
	}

	// No update has run yet, so there is no snapshot to roll back to.
	if _, err := s.Rollback("p"); err == nil {
		t.Fatal("rollback without a snapshot must fail")
	}
	if got := rev(); got != base {
		t.Fatalf("revision after refused rollback = %d, want %d", got, base)
	}

	if _, err := s.Update("p", filepath.Join(sameSrc, "p")); err == nil {
		t.Fatal("update to a version that is not newer must fail")
	}
	if got := rev(); got != base {
		t.Fatalf("revision after refused update = %d, want %d", got, base)
	}

	if err := s.Uninstall("bundled"); err == nil {
		t.Fatal("uninstalling a builtin plugin must fail")
	}
	if got := rev(); got != base {
		t.Fatalf("revision after refused builtin uninstall = %d, want %d", got, base)
	}

	if _, err := s.InstallZip(freshZip); err != nil {
		t.Fatalf("InstallZip: %v", err)
	}
	base++
	if got := rev(); got != base {
		t.Fatalf("revision after zip install = %d, want %d", got, base)
	}
	if _, err := s.UpdateZip("p", newZip); err != nil {
		t.Fatalf("UpdateZip: %v", err)
	}
	base++
	if got := rev(); got != base {
		t.Fatalf("revision after zip update = %d, want %d", got, base)
	}
}
