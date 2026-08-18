package tui

import (
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// Reasoning effort values accepted by /think. They mirror flowcraft's
// inference.ReasoningEffort enum.
const (
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
)

// defaultEffort is applied when no persisted setting exists.
const defaultEffort = EffortMedium

// settings is the TUI's persisted user preferences
// (~/.opencraft/config/tui.yaml). It deliberately lives outside the
// deploy document (opencraft.yaml): the deployment schema only
// understands version/resources/agents/runtime, while UI preferences
// are app-owned.
type settings struct {
	ReasoningEffort string `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty"`
}

// effortOrDefault validates v and falls back to the default.
func effortOrDefault(v string) string {
	switch v {
	case EffortLow, EffortMedium, EffortHigh:
		return v
	default:
		return defaultEffort
	}
}

// settingsPath is the persisted settings file under the user config
// directory.
func settingsPath(configDir string) string {
	return filepath.Join(configDir, "tui.yaml")
}

// loadSettings reads the persisted TUI settings. Missing or corrupt
// files fall back to defaults; corrupt files are ignored rather than
// breaking startup (the next /think write repairs them).
func loadSettings(configDir string) settings {
	var s settings
	if configDir == "" {
		return settings{ReasoningEffort: defaultEffort}
	}
	data, err := os.ReadFile(settingsPath(configDir))
	if err != nil {
		return settings{ReasoningEffort: defaultEffort}
	}
	if err := yaml.Unmarshal(data, &s); err != nil {
		return settings{ReasoningEffort: defaultEffort}
	}
	s.ReasoningEffort = effortOrDefault(s.ReasoningEffort)
	return s
}

// saveSettings persists the TUI settings atomically (write temp +
// rename), creating the config directory when needed.
func saveSettings(configDir string, s settings) error {
	if configDir == "" {
		return nil
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	path := settingsPath(configDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
