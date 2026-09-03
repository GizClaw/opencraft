package desktop

import (
	"context"
	"fmt"
	"os"
	"runtime"

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
	quitConfirmed := a.quitConfirmed
	a.mu.Unlock()

	if quitting && quitConfirmed {
		// Already asked and accepted; let Wails terminate.
		return false
	}
	if quitting || !closeToTray {
		// Every real exit goes through the warning: tray Quit,
		// Cmd+Q/Dock Quit on macOS, and window closes configured to
		// quit. Cancel keeps the app running (and clears the tray /
		// macOS quit request so a later close hides to tray again).
		if !a.confirmQuit(ctx) {
			if quitting {
				a.clearQuitRequest()
			}
			return true
		}
		a.mu.Lock()
		a.quitting = true
		a.quitConfirmed = true
		a.mu.Unlock()
		return false
	}

	// Hide to background. runtime.Hide is exactly the per-platform
	// semantics we want: macOS hides the whole application ([NSApp
	// hide], the default macOS scheme) and Windows/Linux hide the
	// window, leaving the process alive in the tray.
	wailsruntime.Hide(ctx)
	return true
}

// confirmQuit shows the exit warning and reports whether the user chose
// to continue. A nil context (defensive; Startup has not run yet)
// cannot show a dialog, so the quit is allowed.
func (a *App) confirmQuit(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	texts := a.desktopTexts()
	buttons := []string{texts.quitDialogConfirm, texts.quitDialogCancel}
	defaultButton := texts.quitDialogCancel
	cancelButton := texts.quitDialogCancel
	if runtime.GOOS != "darwin" {
		// Wails' Windows/Linux question dialogs are native Yes/No
		// boxes; default to No so Enter does not quit by accident.
		buttons = []string{"Yes", "No"}
		defaultButton = "No"
		cancelButton = "No"
	}
	selection, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.QuestionDialog,
		Title:         texts.quitDialogTitle,
		Message:       texts.quitDialogMessage,
		Buttons:       buttons,
		DefaultButton: defaultButton,
		CancelButton:  cancelButton,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: exit confirmation dialog: %v\n", err)
		return false
	}
	// Windows and Linux return "Yes"/"No"; macOS returns the button
	// label from Buttons.
	return selection == texts.quitDialogConfirm || selection == "Yes"
}

// MarkQuitting records that the user requested to terminate the app
// (tray Quit, or Cmd+Q / Dock Quit on macOS). The confirmation dialog
// is shown by the next CloseRequested round; cancelling clears the
// request so ordinary window closes keep their close-to-tray behaviour.
func (a *App) MarkQuitting() {
	a.mu.Lock()
	a.quitting = true
	a.quitConfirmed = false
	a.mu.Unlock()
}

// clearQuitRequest resets an unconfirmed quit request after the user
// cancels the exit warning.
func (a *App) clearQuitRequest() {
	a.mu.Lock()
	a.quitting = false
	a.quitConfirmed = false
	a.mu.Unlock()
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
		// Hidden to tray, or the user cancelled the exit warning.
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
	ctx := a.ctx
	if ctx == nil {
		// Tray actions only exist after Startup, so this is defensive.
		return
	}
	a.MarkQuitting()
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
	return a.persistDesktopPrefs()
}
