package bindings

import "github.com/GizClaw/opencraft/internal/adapters/desktop/core"

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
// starts or stops the roaming pet window through the desktop root's
// change listener.
func (b *Lifecycle) SetPetsSettings(settings PetsSettings) error {
	if err := b.core.Shell.SetPetsEnabled(settings.Enabled); err != nil {
		return err
	}
	if err := b.core.Shell.SetAssistantPetCharacter(
		settings.AssistantCharacter,
	); err != nil {
		return err
	}
	b.core.Shell.Emit("pet:settings_changed", settings)
	return nil
}

// ReportUserActivity records main-window activity (pointer/keys/focus)
// so the pet mind can tell when the user is around.
func (b *Lifecycle) ReportUserActivity() {
	b.core.Shell.MarkUserActive()
}
