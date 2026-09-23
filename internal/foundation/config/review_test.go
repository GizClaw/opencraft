package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReviewResolveDefaultsAndBounds pins the review knobs: the feature
// ships off, a zero cadence/budget means the shipped default, and
// min_tool_calls / on_failure are not invented by Resolve (the deploy
// document owns them, so a zero there stays zero).
func TestReviewResolveDefaultsAndBounds(t *testing.T) {
	defaults := DefaultReviewSettings()
	if defaults.Enabled == nil || *defaults.Enabled {
		t.Fatalf("review must ship off, got enabled=%v", defaults.Enabled)
	}
	if defaults.EveryTurns != ReviewDefaultEveryTurns ||
		defaults.MinToolCalls != ReviewDefaultMinToolCalls ||
		!defaults.OnFailure ||
		defaults.MaxSuggestions != ReviewDefaultMaxSuggestions ||
		defaults.TimeoutSeconds != ReviewDefaultTimeoutSeconds {
		t.Fatalf("shipped review settings = %+v", defaults)
	}

	resolved, err := ReviewSettings{}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Enabled {
		t.Fatal("a nil enabled must resolve to off (opt-in feature)")
	}
	if resolved.EveryTurns != ReviewDefaultEveryTurns ||
		resolved.MaxSuggestions != ReviewDefaultMaxSuggestions ||
		resolved.TimeoutSeconds != ReviewDefaultTimeoutSeconds {
		t.Fatalf("resolved = %+v, want the defaults", resolved)
	}
	if resolved.MinToolCalls != 0 || resolved.OnFailure {
		t.Fatalf("Resolve invented min_tool_calls/on_failure: %+v", resolved)
	}

	on, err := ReviewSettings{Enabled: boolPtr(true)}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !on.Enabled {
		t.Fatal("an explicit true must survive Resolve")
	}

	for _, bad := range []ReviewSettings{
		{EveryTurns: -1},
		{EveryTurns: ReviewMaxEveryTurns + 1},
		{MinToolCalls: -1},
		{MinToolCalls: ReviewMaxMinToolCalls + 1},
		{MaxSuggestions: -1},
		{MaxSuggestions: ReviewMaxMaxSuggestions + 1},
		{TimeoutSeconds: -1},
		{TimeoutSeconds: ReviewMaxTimeoutSeconds + 1},
	} {
		if _, err := bad.Resolve(); err == nil {
			t.Fatalf("Resolve(%+v) accepted an out-of-range knob", bad)
		}
	}

	if _, err := (ReviewSettings{
		EveryTurns:     ReviewMinEveryTurns,
		MinToolCalls:   ReviewMaxMinToolCalls,
		MaxSuggestions: ReviewMaxMaxSuggestions,
		TimeoutSeconds: ReviewMaxTimeoutSeconds,
	}).Resolve(); err != nil {
		t.Fatalf("the bounds themselves must be accepted: %v", err)
	}
}

// TestReviewUserLayerRoundTrip pins Load -> Save -> Load for the review
// resource: the embedded default is off, a save is readable back, and a
// refused save leaves the stored layer alone.
func TestReviewUserLayerRoundTrip(t *testing.T) {
	dir := t.TempDir()

	embedded, err := LoadReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.Enabled == nil || *embedded.Enabled {
		t.Fatalf("embedded review must default off, got %v", embedded.Enabled)
	}
	if embedded.EveryTurns != ReviewDefaultEveryTurns ||
		embedded.MinToolCalls != ReviewDefaultMinToolCalls ||
		!embedded.OnFailure ||
		embedded.MaxSuggestions != ReviewDefaultMaxSuggestions ||
		embedded.TimeoutSeconds != ReviewDefaultTimeoutSeconds {
		t.Fatalf("embedded review settings = %+v", embedded)
	}

	saved := ReviewSettings{
		Enabled:        boolPtr(true),
		EveryTurns:     10,
		MinToolCalls:   2,
		OnFailure:      true,
		MaxSuggestions: 5,
		TimeoutSeconds: 120,
	}
	if err := SaveReview(dir, saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Enabled == nil || !*loaded.Enabled {
		t.Fatalf("enabled = %v, want the saved true", loaded.Enabled)
	}
	if loaded.EveryTurns != 10 || loaded.MinToolCalls != 2 ||
		!loaded.OnFailure || loaded.MaxSuggestions != 5 ||
		loaded.TimeoutSeconds != 120 {
		t.Fatalf("loaded = %+v, want the saved knobs", loaded)
	}

	if err := SaveReview(dir, ReviewSettings{EveryTurns: ReviewMaxEveryTurns + 1}); err == nil {
		t.Fatal("SaveReview must refuse out-of-range settings")
	}
	again, err := LoadReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.EveryTurns != 10 {
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
