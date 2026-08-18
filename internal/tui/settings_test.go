package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEffortOrDefault(t *testing.T) {
	cases := map[string]string{
		"":       EffortMedium,
		"low":    EffortLow,
		"medium": EffortMedium,
		"high":   EffortHigh,
		"max":    EffortMedium,
	}
	for in, want := range cases {
		if got := effortOrDefault(in); got != want {
			t.Errorf("effortOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := saveSettings(dir, settings{ReasoningEffort: EffortHigh}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tui.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(dir).ReasoningEffort; got != EffortHigh {
		t.Errorf("loaded effort = %q, want %q (file: %s)", got, EffortHigh, data)
	}
}

func TestLoadSettingsFallbacks(t *testing.T) {
	// Missing file and corrupt file both fall back to the default
	// instead of breaking startup.
	dir := t.TempDir()
	if got := loadSettings(dir).ReasoningEffort; got != defaultEffort {
		t.Errorf("missing file effort = %q, want %q", got, defaultEffort)
	}
	if err := os.WriteFile(filepath.Join(dir, "tui.yaml"),
		[]byte("not: [valid: yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(dir).ReasoningEffort; got != defaultEffort {
		t.Errorf("corrupt file effort = %q, want %q", got, defaultEffort)
	}
}
