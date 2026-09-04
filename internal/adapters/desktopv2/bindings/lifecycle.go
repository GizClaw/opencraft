package bindings

import "github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"

// Lifecycle exposes native window/tray lifecycle methods.
type Lifecycle struct {
	core *core.Core
}

// NewLifecycle wires the lifecycle binding to the core shell.
func NewLifecycle(c *core.Core) *Lifecycle {
	return &Lifecycle{core: c}
}

// CloseRequested returns true to cancel a window close (hide) or false
// to let Wails terminate.
func (b *Lifecycle) CloseRequested() bool {
	ctx := b.core.Shell.Context()
	return b.core.Shell.CloseRequested(ctx)
}

// MarkQuitting records an unconfirmed quit request.
func (b *Lifecycle) MarkQuitting() {
	b.core.Shell.MarkQuitting()
}

// RequestClose drives the custom title-bar close button.
func (b *Lifecycle) RequestClose() {
	b.core.Shell.RequestClose()
}

// ShowMainWindow restores the main window.
func (b *Lifecycle) ShowMainWindow() {
	b.core.Shell.ShowMainWindow()
}

// QuitFromTray terminates the app from the tray menu.
func (b *Lifecycle) QuitFromTray() {
	b.core.Shell.QuitFromTray()
}

// GetCloseToTray reports whether close hides instead of quitting.
func (b *Lifecycle) GetCloseToTray() bool {
	return b.core.Shell.GetCloseToTray()
}

// SetCloseToTray persists the close behavior.
func (b *Lifecycle) SetCloseToTray(closeToTray bool) error {
	return b.core.Shell.SetCloseToTray(closeToTray)
}

// GetLanguage returns the current native desktop language.
func (b *Lifecycle) GetLanguage() string {
	return b.core.Shell.Language()
}

// SetLanguage persists the native desktop language.
func (b *Lifecycle) SetLanguage(language string) error {
	return b.core.Shell.SetLanguage(language)
}
