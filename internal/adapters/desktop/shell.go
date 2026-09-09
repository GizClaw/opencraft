package desktop

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Shell is the Wails v3 lifecycle service for the desktop composition root.
// The entry point registers it with application.NewService so Startup and
// Shutdown run inside the application lifecycle. The window helper the entry
// point needs is a package-level function below: it must never be exposed as
// a frontend-callable binding.
type Shell struct {
	app     *application.App
	main    *application.WebviewWindow
	desktop *Desktop
}

// NewShell wires the service to the v3 application and the shared desktop
// composition root.
func NewShell(app *application.App, d *Desktop) *Shell {
	return &Shell{app: app, desktop: d}
}

// ServiceName identifies the lifecycle service in the Wails runtime. The
// value is used for logging and error attribution only; the Shell type no
// longer exposes frontend-callable methods, so the identity is the desktop
// app itself rather than the legacy "shell operations" binding surface.
func (s *Shell) ServiceName() string { return "Desktop" }

// ServiceStartup runs the shared composition-root startup with the v3
// application context.
func (s *Shell) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.desktop.Startup(ctx)
	return nil
}

// ServiceShutdown tears the composition root down in reverse service order.
func (s *Shell) ServiceShutdown() error {
	s.desktop.Shutdown(context.Background())
	return nil
}

// setMain records the main window handle after the entry point creates it.
func (s *Shell) setMain(w *application.WebviewWindow) {
	s.main = w
	s.desktop.core.Shell.Attach(s.app, w)
}

// SetMainWindow records the main window handle on the shell service. It is a
// package-level helper (not a bound service method): only the entry point
// attaches the window, so the frontend must not be able to invoke it.
func SetMainWindow(s *Shell, w *application.WebviewWindow) {
	if s == nil {
		return
	}
	s.setMain(w)
}

// ShowMainWindow restores and focuses the main window for tray, Dock reopen
// and second-instance launch. Package-level helper: these activations are
// Go-side entry points, not frontend bindings.
func ShowMainWindow(s *Shell) {
	if s == nil || s.main == nil {
		return
	}
	s.main.Show()
	s.main.Focus()
}
