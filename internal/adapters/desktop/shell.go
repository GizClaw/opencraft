package desktop

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/version"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Shell is the Wails v3 UI service for shell-level operations. Domain
// services live in the sibling desktop/bindings package and are registered
// alongside this service by the application entry.
type Shell struct {
	app     *application.App
	main    *application.WebviewWindow
	second  *application.WebviewWindow
	desktop *Desktop
}

// NewShell wires the service to the v3 application and the shared desktop
// composition root.
func NewShell(app *application.App, d *Desktop) *Shell {
	return &Shell{app: app, desktop: d}
}

// ServiceName keeps the generated binding name stable.
func (s *Shell) ServiceName() string { return "Shell" }

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

// SetMain records the main window handle after the entry point creates it.
func (s *Shell) SetMain(w *application.WebviewWindow) {
	s.main = w
	s.desktop.core.Shell.Attach(s.app, w)
}

// Emit pushes one log line through the shared event bus used by the v3 UI.
func (s *Shell) Emit(format string, args ...any) {
	s.app.Event.Emit("v3:log", fmt.Sprintf("[%s] %s",
		time.Now().Format("15:04:05"), fmt.Sprintf(format, args...)))
}

// ShowMainWindow restores the main window (tray, Dock reopen, second launch).
func (s *Shell) ShowMainWindow() {
	if s.main == nil {
		return
	}
	s.main.Show()
	s.main.Focus()
}

// Version reports the injected build version.
func (s *Shell) Version() string {
	return version.ServiceVersion
}

// WindowInfo lists live windows so lifecycle tests can assert state.
func (s *Shell) WindowInfo() string {
	var out string
	for _, w := range s.app.Window.GetAll() {
		out += fmt.Sprintf("id=%d name=%s visible=%v ", w.ID(), w.Name(), w.IsVisible())
	}
	return out
}

// OpenSecondWindow opens a second webview window on demand.
func (s *Shell) OpenSecondWindow() string {
	if s.second != nil {
		s.second.Show()
		s.second.Focus()
		return "second window already open; shown"
	}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "second",
		Title:            "OpenCraft second window",
		Width:            680,
		Height:           480,
		URL:              "/#second",
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
		BackgroundColour: application.NewRGB(15, 18, 24),
	})
	s.second = w
	s.Emit("opened second window id=%d name=%s", w.ID(), w.Name())
	return "second window opened"
}

// CloseSecondWindow closes the second window for real.
func (s *Shell) CloseSecondWindow() string {
	if s.second == nil {
		return "no second window"
	}
	w := s.second
	s.second = nil
	w.Close()
	s.Emit("closed second window")
	return "second window closed"
}

// Broadcast emits one application-wide event from Go.
func (s *Shell) Broadcast(msg string) string {
	s.Emit("Go broadcast: %s", msg)
	return "broadcast emitted"
}

// ReportProbe is the automation sink used by the self-driving frontend.
func (s *Shell) ReportProbe(tag, value string) string {
	log.Printf("V3AUTO %s = %q", tag, value)
	return "reported"
}

// Quit requests application shutdown through the v3 lifecycle.
func (s *Shell) Quit() string {
	s.Emit("quitting from UI")
	s.desktop.RequestQuit()
	return "quit requested"
}
