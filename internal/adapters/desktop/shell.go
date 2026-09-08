//go:build wails3

package desktop

import (
	"fmt"
	"log"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/version"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Shell is the minimal v3 service. It is a placeholder for the domain
// bindings that Phase 2 will migrate from desktopv2 one group at a time.
type Shell struct {
	app    *application.App
	main   *application.WebviewWindow
	second *application.WebviewWindow
}

// NewShell wires the service to the application instance created by the v3
// entry point. The main window is attached after creation so window managers
// can be used by the service before Run starts.
func NewShell(app *application.App) *Shell {
	return &Shell{app: app}
}

// ServiceName keeps the generated binding name stable.
func (s *Shell) ServiceName() string { return "Shell" }

// SetMain records the main window handle after the entry point creates it.
func (s *Shell) SetMain(w *application.WebviewWindow) {
	s.main = w
}

// ShowMainWindow restores the main window (tray, Dock reopen, second launch).
func (s *Shell) ShowMainWindow() {
	if s.main == nil {
		return
	}
	s.main.Show()
	s.main.Focus()
}

// Emit pushes one log line through the shared event bus used by the v3 UI.
func (s *Shell) Emit(format string, args ...any) {
	s.app.Event.Emit("v3:log", fmt.Sprintf("[%s] %s",
		time.Now().Format("15:04:05"), fmt.Sprintf(format, args...)))
}

// Version reports the injected build version (same -X target as v2).
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

// OpenSecondWindow opens a second webview window on demand (multi-window
// smoke path for the migration skeleton).
func (s *Shell) OpenSecondWindow() string {
	if s.second != nil {
		s.second.Show()
		s.second.Focus()
		return "second window already open; shown"
	}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "second",
		Title:            "OpenCraft v3 second window",
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

// Broadcast emits one application-wide event from Go so both windows can log
// that they received it.
func (s *Shell) Broadcast(msg string) string {
	s.Emit("Go broadcast: %s", msg)
	return "broadcast emitted"
}

// ReportProbe is the automation sink used by the self-driving frontend:
// it lets non-UI runs assert that bindings and events work end to end.
func (s *Shell) ReportProbe(tag, value string) string {
	log.Printf("V3AUTO %s = %q", tag, value)
	return "reported"
}

// Quit requests application shutdown through the v3 lifecycle.
func (s *Shell) Quit() string {
	s.Emit("quitting from UI")
	s.app.Quit()
	return "quit requested"
}
