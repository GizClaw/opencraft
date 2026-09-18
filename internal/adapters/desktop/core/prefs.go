package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
)

const prefsFile = "desktop.json"

// defaultSessionDefaults returns the canonical first-run mode/think
// values shared by the preference document, the conversation service,
// and normalization fallbacks.
func defaultSessionDefaults() (sessions.Mode, string) {
	if profile.YoloOnly() {
		return sessions.ModeYOLO, string(sessions.ThinkMedium)
	}
	return sessions.ModeWorkspace, string(sessions.ThinkMedium)
}

// DesktopPrefs is the persisted desktop preference document.
type DesktopPrefs struct {
	CloseToTray  bool   `json:"closeToTray"`
	Language     string `json:"language,omitempty"`
	DefaultMode  string `json:"defaultMode,omitempty"`
	DefaultThink string `json:"defaultThink,omitempty"`
	// UI carries the appearance preferences (Settings > Interface):
	// interface font, code font and UI scale.
	UI UIPrefs `json:"ui,omitempty"`
	// Pets carries the desktop pet surface preferences. The assistant
	// character id is resolved against the pet pack registry at window
	// creation time; unknown ids fall back to the builtin default.
	Pets PetPrefs `json:"pets,omitempty"`
	// Telemetry carries the plugin telemetry-export switch.
	Telemetry TelemetryPrefs `json:"telemetry,omitempty"`
	// Path carries the user's PATH override (Settings > Diagnostics),
	// applied once at startup by foundation/utils/envpath.
	Path PathPrefs `json:"path,omitempty"`
	// Exec carries the exec supervisor pool knobs (Settings >
	// Diagnostics). Saving applies to future leases; existing children
	// are not restarted.
	Exec ExecPrefs `json:"exec,omitempty"`
}

// ExecPrefs is the desktop preference section that configures the exec
// child pool.
type ExecPrefs struct {
	Prewarm     int `json:"prewarm,omitempty"`
	MaxIdle     int `json:"maxIdle,omitempty"`
	MaxActive   int `json:"maxActive,omitempty"`
	IdleMinutes int `json:"idleMinutes,omitempty"`
}

// PoolSettings converts the preference shape to the execd pool shape.
func (p ExecPrefs) PoolSettings() execd.PoolSettings {
	if p.Prewarm == 0 && p.MaxIdle == 0 &&
		p.MaxActive == 0 && p.IdleMinutes == 0 {
		return execd.DefaultPoolSettings()
	}
	return execd.NormalizePoolSettings(execd.PoolSettings{
		Prewarm:   p.Prewarm,
		MaxIdle:   p.MaxIdle,
		MaxActive: p.MaxActive,
		IdleTTL:   time.Duration(p.IdleMinutes) * time.Minute,
	})
}

// PoolPrefs renders pool settings back into the preference shape.
func PoolPrefs(settings execd.PoolSettings) ExecPrefs {
	settings = execd.NormalizePoolSettings(settings)
	return ExecPrefs{
		Prewarm:     settings.Prewarm,
		MaxIdle:     settings.MaxIdle,
		MaxActive:   settings.MaxActive,
		IdleMinutes: int(settings.IdleTTL / time.Minute),
	}
}

// PetPrefs is the desktop pet section of the preference document.
type PetPrefs struct {
	Enabled            bool   `json:"enabled"`
	AssistantCharacter string `json:"assistantCharacter,omitempty"`
}

// TelemetryPrefs is the desktop telemetry section of the preference
// document.
type TelemetryPrefs struct {
	// PluginExport allows capability plugins that declare
	// telemetry:export to install their own OTLP export sink. On by
	// default, because installing such a plugin is already an explicit
	// user decision; turning it off makes the host refuse every
	// telemetry.configure call instead of trusting the manifest.
	PluginExport bool `json:"pluginExport"`
}

// LoadPrefs reads the desktop preference file with defaults.
func LoadPrefs(userDir string) DesktopPrefs {
	prefs := DefaultPrefs()
	if userDir == "" {
		if dir, err := config.UserConfigDir(); err == nil {
			userDir = dir
		} else {
			telemetry.WarnErr(context.Background(),
				"desktop prefs: resolve config dir failed", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(userDir, prefsFile))
	if errors.Is(err, os.ErrNotExist) {
		return prefs
	}
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop prefs: read prefs failed", err)
		return prefs
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop prefs: decode prefs failed", err)
		return prefs
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop prefs: re-encode prefs failed", err)
		return prefs
	}
	if err := json.Unmarshal(raw, &prefs); err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop prefs: decode typed prefs failed", err)
	}
	return normalizePrefs(prefs)
}

// DefaultPrefs returns the preference document with the canonical
// first-run defaults: closing hides to tray, sessions start in
// workspace mode with medium reasoning.
func DefaultPrefs() DesktopPrefs {
	mode, think := defaultSessionDefaults()
	return DesktopPrefs{
		CloseToTray:  true,
		DefaultMode:  string(mode),
		DefaultThink: think,
		UI:           defaultUIPrefs(),
		Telemetry:    TelemetryPrefs{PluginExport: true},
		Exec:         PoolPrefs(execd.DefaultPoolSettings()),
	}
}

// normalizePrefs repairs values that became invalid (or are missing
// from older preference files) back to the canonical defaults.
func normalizePrefs(prefs DesktopPrefs) DesktopPrefs {
	defaults := DefaultPrefs()
	prefs.UI = normalizeUIPrefs(prefs.UI)
	prefs.Path = normalizePathPrefs(prefs.Path)
	prefs.Exec = PoolPrefs(prefs.Exec.PoolSettings())
	// The yoloonly build has a single available mode: repair any
	// preference document (possibly written by the regular build) so
	// new sessions cannot start confined.
	if profile.YoloOnly() {
		prefs.DefaultMode = string(sessions.ModeYOLO)
	}
	switch sessions.Mode(prefs.DefaultMode) {
	case sessions.ModeWorkspace, sessions.ModeReadOnly, sessions.ModeYOLO:
	default:
		prefs.DefaultMode = defaults.DefaultMode
	}
	if !sessions.ThinkLevel(prefs.DefaultThink).Valid() {
		prefs.DefaultThink = defaults.DefaultThink
	}
	return prefs
}

// SavePrefs atomically writes the preference file.
func SavePrefs(userDir string, prefs DesktopPrefs) error {
	if userDir == "" {
		var err error
		userDir, err = config.UserConfigDir()
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(userDir, prefsFile)
	tmp, err := os.CreateTemp(userDir, ".desktop-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !os.IsNotExist(err) {
			telemetry.WarnErr(context.Background(),
				"desktop prefs: remove prefs temp failed", err)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop prefs: close prefs temp after write failure", tmp.Close())
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
