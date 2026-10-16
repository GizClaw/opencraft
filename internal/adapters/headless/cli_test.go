package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepairConfigFlagRemovesRetiredRefs pins the headless repair path
// for retired ${env:OPEN_CRAFT_*} references: such a user layer fails
// every load, and a headless-only deployment has no settings page to
// offer the desktop Diagnostics button on.
func TestRepairConfigFlagRemovesRetiredRefs(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()
	layer := filepath.Join(configDir, "opencraft.yaml")
	broken := "version: v1\n" +
		"agents:\n" +
		"  assistant:\n" +
		"    prepare:\n" +
		"      - root: \"${env:OPEN_CRAFT_WORKDIR}\"\n"
	if err := os.WriteFile(layer, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	// The scan only counts a reference as broken while the retired
	// name is unset; scrub it so the test does not depend on the
	// caller's shell.
	_ = os.Unsetenv("OPEN_CRAFT_WORKDIR")

	code := Main([]string{
		"--config-dir", configDir,
		"--data-dir", dataDir,
		"--repair-config",
	})
	if code != 0 {
		t.Fatalf("Main(--repair-config) = %d, want 0", code)
	}
	repaired, err := os.ReadFile(layer)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(repaired), "OPEN_CRAFT_WORKDIR") {
		t.Fatalf("retired reference survived the repair:\n%s", repaired)
	}
	if _, err := os.Stat(layer + ".bak"); err != nil {
		t.Fatalf("pre-repair copy missing: %v", err)
	}

	// A second repair is a no-op, not an error.
	if code := Main([]string{
		"--config-dir", configDir,
		"--data-dir", dataDir,
		"--repair-config",
	}); code != 0 {
		t.Fatalf("second Main(--repair-config) = %d, want 0", code)
	}
}
