package bindings

import (
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/sysfont"
)

// Lifecycle exposes native window/tray lifecycle methods.
type Lifecycle struct {
	core *core.Core
}

// NewLifecycle wires the lifecycle binding to the core shell.
func NewLifecycle(c *core.Core) *Lifecycle {
	return &Lifecycle{core: c}
}

// RequestClose drives the custom title-bar close button.
func (b *Lifecycle) RequestClose() {
	b.core.Shell.RequestClose()
}

// GetCloseToTray reports whether close hides instead of quitting.
func (b *Lifecycle) GetCloseToTray() bool {
	return b.core.Shell.GetCloseToTray()
}

// SetCloseToTray persists the close behavior.
func (b *Lifecycle) SetCloseToTray(closeToTray bool) error {
	return b.core.Shell.SetCloseToTray(closeToTray)
}

// SetLanguage persists the native desktop language.
func (b *Lifecycle) SetLanguage(language string) error {
	return b.core.Shell.SetLanguage(language)
}

// PetsSettings is the app-wide desktop pet preference surface.
type PetsSettings struct {
	Enabled            bool   `json:"enabled"`
	AssistantCharacter string `json:"assistantCharacter,omitempty"`
}

// GetPetsSettings returns the persisted pet preferences.
func (b *Lifecycle) GetPetsSettings() PetsSettings {
	return PetsSettings{
		Enabled:            b.core.Shell.PetsEnabled(),
		AssistantCharacter: b.core.Shell.AssistantPetCharacter(),
	}
}

// SetPetsSettings persists the pet preferences. Toggling enabled
// starts or stops the pet window through the desktop root's change
// listener.
func (b *Lifecycle) SetPetsSettings(settings PetsSettings) error {
	if err := b.core.Shell.SetPetsEnabled(settings.Enabled); err != nil {
		return err
	}
	if err := b.core.Shell.SetAssistantPetCharacter(
		settings.AssistantCharacter,
	); err != nil {
		return err
	}
	b.core.Shell.Emit(core.EventPetSettingsChanged, settings)
	return nil
}

// UISettings is the desktop interface preference surface (Settings >
// Interface). A font is a preset id plus, for "custom", the family name from
// the system catalogue; the renderer turns that into a CSS font stack (see
// frontend/src/lib/appearance.ts). Accent is the highlight-colour preset id.
// ShowHiddenFiles drives the workspace tree's dotfile switch.
type UISettings struct {
	FontFamily      string  `json:"fontFamily"`
	FontFamilyName  string  `json:"fontFamilyName,omitempty"`
	CodeFont        string  `json:"codeFont"`
	CodeFontName    string  `json:"codeFontName,omitempty"`
	FontScale       float64 `json:"fontScale"`
	ShowHiddenFiles bool    `json:"showHiddenFiles"`
	// Accent is the highlight-colour preset id (blue/violet/teal/orange/
	// rose): the renderer re-points --color-accent at it.
	Accent string `json:"accent"`
	// GitMarks draws the file viewer's per-line git change marks. A
	// missing value means enabled (the default), so only the explicit
	// false a user picked is ever absent from the document.
	GitMarks *bool `json:"gitMarks,omitempty"`
}

// GetUISettings returns the persisted appearance preferences.
func (b *Lifecycle) GetUISettings() UISettings {
	ui := b.core.Shell.UISettings()
	return UISettings{
		FontFamily:      ui.FontFamily,
		FontFamilyName:  ui.FontFamilyName,
		CodeFont:        ui.CodeFont,
		CodeFontName:    ui.CodeFontName,
		FontScale:       ui.FontScale,
		ShowHiddenFiles: ui.ShowHiddenFiles,
		Accent:          ui.Accent,
		GitMarks:        ui.GitMarks,
	}
}

// SetUISettings persists the appearance preferences. The renderer applies
// them immediately; the desktop document is the durable copy.
func (b *Lifecycle) SetUISettings(settings UISettings) error {
	return b.core.Shell.SetUISettings(core.UIPrefs{
		FontFamily:      settings.FontFamily,
		FontFamilyName:  settings.FontFamilyName,
		CodeFont:        settings.CodeFont,
		CodeFontName:    settings.CodeFontName,
		FontScale:       settings.FontScale,
		ShowHiddenFiles: settings.ShowHiddenFiles,
		Accent:          settings.Accent,
		GitMarks:        settings.GitMarks,
	})
}

// ListFonts returns the font families installed on this machine, sorted and
// deduplicated. An empty list means the platform exposes no catalogue here;
// the picker then falls back to typed family names.
func (b *Lifecycle) ListFonts() []string {
	families, err := sysfont.List()
	if err != nil {
		telemetry.WarnErr(b.core.Shell.Context(),
			"desktop appearance: list system fonts failed", err)
	}
	return families
}

// ReportUserActivity records main-window activity (pointer/keys/focus)
// so the pet mind can tell when the user is around.
func (b *Lifecycle) ReportUserActivity() {
	b.core.Shell.MarkUserActive()
}
