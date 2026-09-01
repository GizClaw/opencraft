package desktop

import (
	"context"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// CloseRequested is the single funnel for every window-close path:
// the native close button (Windows WM_CLOSE / Linux delete-event /
// macOS traffic light), the custom title-bar X button, and Cmd+Q /
// Dock Quit on macOS all reach it via OnBeforeClose. It returns true
// to cancel the close (hide instead) or false to let Wails quit.
func (a *App) CloseRequested(ctx context.Context) bool {
	a.mu.Lock()
	closeToTray := a.closeToTray
	quitting := a.quitting
	a.mu.Unlock()
	if quitting {
		// The user chose Quit from the tray menu; let Wails terminate.
		return false
	}
	if !closeToTray {
		return false
	}
	// Hide to background. runtime.Hide is exactly the per-platform
	// semantics we want: macOS hides the whole application ([NSApp
	// hide], the default macOS scheme) and Windows/Linux hide the
	// window, leaving the process alive in the tray.
	wailsruntime.Hide(ctx)
	return true
}

// RequestClose is the JS binding for the custom title-bar X button. It
// behaves identically to the native close path (CloseRequested), so the
// "close to tray" setting applies no matter how the window is closed.
func (a *App) RequestClose() {
	ctx := a.ctx
	if ctx == nil {
		// Startup has not run yet; nothing to hide or quit.
		return
	}
	if a.CloseRequested(ctx) {
		// Hidden to tray; the process keeps running.
		return
	}
	// Direct-quit mode (or already quitting): terminate the app.
	wailsruntime.Quit(ctx)
}

// ShowMainWindow restores the main window after it was hidden or
// minimised. It is the second-instance handler (launcher/Dock click
// while the app runs in the background) and the tray "Show" action.
func (a *App) ShowMainWindow() {
	ctx := a.ctx
	if ctx == nil {
		// Startup has not run yet: the window is about to appear
		// anyway, so there is nothing to restore.
		return
	}
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.WindowShow(ctx)
	// macOS: the app may be hidden via [NSApp hide]; Show() unhides it.
	// Windows/Linux: Show() is the same as WindowShow.
	wailsruntime.Show(ctx)
}

// QuitFromTray terminates the app from the tray menu. The quitting flag
// must be set before runtime.Quit because Quit also passes through
// OnBeforeClose, which would otherwise hide the app again.
func (a *App) QuitFromTray() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		// Tray actions only exist after Startup, so this is defensive.
		return
	}
	wailsruntime.Quit(ctx)
}

// GetCloseToTray reports the persisted close behaviour (true: hide to
// tray on close; false: quit).
func (a *App) GetCloseToTray() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closeToTray, nil
}

// SetCloseToTray updates and persists the close behaviour. The change
// takes effect immediately for subsequent closes.
func (a *App) SetCloseToTray(closeToTray bool) error {
	a.mu.Lock()
	a.closeToTray = closeToTray
	a.mu.Unlock()
	userDir := a.userDir
	return savePrefs(userDir, desktopPrefs{CloseToTray: closeToTray})
}
