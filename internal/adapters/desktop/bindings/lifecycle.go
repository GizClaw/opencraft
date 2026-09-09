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
