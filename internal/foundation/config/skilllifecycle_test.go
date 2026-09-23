package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSkillLifecycleResolveDefaultsAndBounds pins the curator
// thresholds: recording ships on, a zero threshold means the shipped
// default, and out-of-range values are refused.
func TestSkillLifecycleResolveDefaultsAndBounds(t *testing.T) {
	defaults := DefaultSkillLifecycleSettings()
	if defaults.Enabled == nil || !*defaults.Enabled {
		t.Fatalf("shipped enabled = %v, want usage recording on", defaults.Enabled)
	}
	if defaults.StaleAfterDays != SkillLifecycleDefaultStaleAfterDays ||
		defaults.MinUses != SkillLifecycleDefaultMinUses ||
		defaults.UsageWindowDays != SkillLifecycleDefaultUsageWindowDays {
		t.Fatalf("shipped thresholds = %+v", defaults)
	}

	resolved, err := SkillLifecycleSettings{}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Enabled {
		t.Fatal("a nil enabled must resolve to on")
	}
	if resolved.StaleAfterDays != SkillLifecycleDefaultStaleAfterDays ||
		resolved.MinUses != SkillLifecycleDefaultMinUses ||
		resolved.UsageWindowDays != SkillLifecycleDefaultUsageWindowDays {
		t.Fatalf("resolved = %+v, want the defaults", resolved)
	}

	off, err := SkillLifecycleSettings{Enabled: boolPtr(false)}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled {
		t.Fatal("an explicit false must survive Resolve")
	}

	for _, bad := range []SkillLifecycleSettings{
		{StaleAfterDays: -1},
		{StaleAfterDays: SkillLifecycleMaxStaleAfterDays + 1},
		{MinUses: -1},
		{MinUses: SkillLifecycleMaxMinUses + 1},
		{UsageWindowDays: -1},
		{UsageWindowDays: SkillLifecycleMaxUsageWindowDays + 1},
	} {
		if _, err := bad.Resolve(); err == nil {
			t.Fatalf("Resolve(%+v) accepted an out-of-range threshold", bad)
		}
	}

	if _, err := (SkillLifecycleSettings{
		StaleAfterDays:  SkillLifecycleMinStaleAfterDays,
		MinUses:         SkillLifecycleMinMinUses,
		UsageWindowDays: SkillLifecycleMinUsageWindowDays,
	}).Resolve(); err != nil {
		t.Fatalf("the bounds themselves must be accepted: %v", err)
	}
}

// TestSkillLifecycleUserLayerRoundTrip pins Load -> Save -> Load for the
// skilllifecycle resource and that a refused save leaves the stored
// layer alone.
func TestSkillLifecycleUserLayerRoundTrip(t *testing.T) {
	dir := t.TempDir()

	embedded, err := LoadSkillLifecycle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.Enabled == nil || !*embedded.Enabled ||
		embedded.StaleAfterDays != SkillLifecycleDefaultStaleAfterDays ||
		embedded.MinUses != SkillLifecycleDefaultMinUses ||
		embedded.UsageWindowDays != SkillLifecycleDefaultUsageWindowDays {
		t.Fatalf("embedded defaults = %+v", embedded)
	}

	saved := SkillLifecycleSettings{
		Enabled:         boolPtr(false),
		StaleAfterDays:  30,
		MinUses:         5,
		UsageWindowDays: 30,
	}
	if err := SaveSkillLifecycle(dir, saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSkillLifecycle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Enabled == nil || *loaded.Enabled {
		t.Fatalf("enabled = %v, want the saved false", loaded.Enabled)
	}
	if loaded.StaleAfterDays != 30 || loaded.MinUses != 5 ||
		loaded.UsageWindowDays != 30 {
		t.Fatalf("loaded = %+v, want the saved thresholds", loaded)
	}

	if err := SaveSkillLifecycle(dir,
		SkillLifecycleSettings{StaleAfterDays: 1}); err == nil {
		t.Fatal("SaveSkillLifecycle must refuse out-of-range settings")
	}
	again, err := LoadSkillLifecycle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.StaleAfterDays != 30 {
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
