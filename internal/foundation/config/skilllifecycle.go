// Skill lifecycle settings.
//
// This file owns the skilllifecycle resource's settings shape, its
// defaults and validation, and the user-layer persistence the skills
// page uses. The curator (capabilities/skills) decodes its factory
// settings with these types, so the page and the runtime cannot drift.
package config

import (
	"fmt"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// ResourceSkillLifecycle is the deploy-document id of the skill
// lifecycle resource.
const ResourceSkillLifecycle = "skilllifecycle"

// Bounds and defaults for the curator thresholds.
const (
	SkillLifecycleDefaultStaleAfterDays = 45
	SkillLifecycleMinStaleAfterDays     = 7
	SkillLifecycleMaxStaleAfterDays     = 365

	SkillLifecycleDefaultMinUses = 3
	SkillLifecycleMinMinUses     = 1
	SkillLifecycleMaxMinUses     = 50

	SkillLifecycleDefaultUsageWindowDays = 90
	SkillLifecycleMinUsageWindowDays     = 14
	SkillLifecycleMaxUsageWindowDays     = 365
)

// SkillLifecycleSettings is the skilllifecycle resource's settings
// subtree.
type SkillLifecycleSettings struct {
	// Enabled turns usage recording on. The curator's thresholds are
	// still readable when recording is off; there is simply no data.
	Enabled *bool `json:"enabled,omitempty"`
	// StaleAfterDays marks a skill idle (a retirement candidate) once
	// it has not been used for this many days.
	StaleAfterDays int `json:"stale_after_days,omitempty"`
	// MinUses is the use count below which an idle skill is called
	// stale; a skill used often enough is never a candidate.
	MinUses int `json:"min_uses,omitempty"`
	// UsageWindowDays bounds the statistics window (and the horizon the
	// curator prunes usage events at).
	UsageWindowDays int `json:"usage_window_days,omitempty"`
}

// SkillLifecycleConfig is the validated, defaulted view.
type SkillLifecycleConfig struct {
	Enabled         bool
	StaleAfterDays  int
	MinUses         int
	UsageWindowDays int
}

// DefaultSkillLifecycleSettings returns the shipped settings.
func DefaultSkillLifecycleSettings() SkillLifecycleSettings {
	enabled := true
	return SkillLifecycleSettings{
		Enabled:         &enabled,
		StaleAfterDays:  SkillLifecycleDefaultStaleAfterDays,
		MinUses:         SkillLifecycleDefaultMinUses,
		UsageWindowDays: SkillLifecycleDefaultUsageWindowDays,
	}
}

// Resolve validates the settings and applies defaults.
func (s SkillLifecycleSettings) Resolve() (SkillLifecycleConfig, error) {
	out := SkillLifecycleConfig{
		Enabled:         s.Enabled == nil || *s.Enabled,
		StaleAfterDays:  s.StaleAfterDays,
		MinUses:         s.MinUses,
		UsageWindowDays: s.UsageWindowDays,
	}
	if out.StaleAfterDays == 0 {
		out.StaleAfterDays = SkillLifecycleDefaultStaleAfterDays
	}
	if out.StaleAfterDays < SkillLifecycleMinStaleAfterDays ||
		out.StaleAfterDays > SkillLifecycleMaxStaleAfterDays {
		return SkillLifecycleConfig{}, fmt.Errorf(
			"skill lifecycle: stale_after_days %d out of range (%d-%d)",
			out.StaleAfterDays,
			SkillLifecycleMinStaleAfterDays, SkillLifecycleMaxStaleAfterDays)
	}
	if out.MinUses == 0 {
		out.MinUses = SkillLifecycleDefaultMinUses
	}
	if out.MinUses < SkillLifecycleMinMinUses ||
		out.MinUses > SkillLifecycleMaxMinUses {
		return SkillLifecycleConfig{}, fmt.Errorf(
			"skill lifecycle: min_uses %d out of range (%d-%d)",
			out.MinUses, SkillLifecycleMinMinUses, SkillLifecycleMaxMinUses)
	}
	if out.UsageWindowDays == 0 {
		out.UsageWindowDays = SkillLifecycleDefaultUsageWindowDays
	}
	if out.UsageWindowDays < SkillLifecycleMinUsageWindowDays ||
		out.UsageWindowDays > SkillLifecycleMaxUsageWindowDays {
		return SkillLifecycleConfig{}, fmt.Errorf(
			"skill lifecycle: usage_window_days %d out of range (%d-%d)",
			out.UsageWindowDays,
			SkillLifecycleMinUsageWindowDays,
			SkillLifecycleMaxUsageWindowDays)
	}
	return out, nil
}

// skillLifecycleLayer is the user-layer document SaveSkillLifecycle
// writes.
type skillLifecycleLayer struct {
	Version   string `json:"version"`
	Resources struct {
		SkillLifecycle *skillLifecycleResourceLayer `json:"skilllifecycle,omitempty"`
	} `json:"resources"`
}

type skillLifecycleResourceLayer struct {
	Settings SkillLifecycleSettings `json:"settings"`
}

// LoadSkillLifecycle returns the effective settings: embedded defaults
// overlaid with the user layer's resources.skilllifecycle.settings.
func LoadSkillLifecycle(configDir string) (SkillLifecycleSettings, error) {
	return layeredResourceSettings[SkillLifecycleSettings](
		configDir, ResourceSkillLifecycle)
}

// SaveSkillLifecycle validates the settings and persists them as the
// user layer's skilllifecycle resource.
func SaveSkillLifecycle(configDir string, settings SkillLifecycleSettings) error {
	if _, err := settings.Resolve(); err != nil {
		return err
	}
	layer := skillLifecycleLayer{Version: "v1"}
	layer.Resources.SkillLifecycle = &skillLifecycleResourceLayer{
		Settings: settings,
	}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render skill lifecycle layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{ResourceSkillLifecycle: true},
		map[string]bool{},
		map[string]bool{},
		false,
	)
	if err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(configDir, "opencraft.yaml"),
		merged,
		0o600,
	)
}
