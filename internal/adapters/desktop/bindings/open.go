package bindings

import (
	"context"
	"os/exec"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// The system hand-off: give a URL or a local path to the OS, either
// with the default app, through the platform "Open With" flow, or
// revealed in the file manager. One place names the program and the
// argv per platform, and one place owns the "started, outlives us"
// tail.
//
// openArgs is a pure table keyed by goos — the same shape as
// shelldetect.Detect and envpath.Options — so every platform branch is
// assertable on any host, and launch is swappable so a test can check
// the argv without opening a window.

type openMode int

const (
	// openDefault opens the target with the system default app.
	openDefault openMode = iota
	// openAs shows the platform's "Open With" chooser.
	openAs
	// reveal highlights the target in the platform file manager.
	reveal
)

// launch runs one hand-off command. The child owns a window and
// outlives us, so the handle is released instead of waited on (a
// no-op on Windows); failing to release is worth a warning, not an
// error the UI should show after the app already opened.
var launch = func(ctx context.Context, program string, args []string) error {
	cmd := exec.Command(program, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	telemetry.WarnErr(ctx,
		"file: release system hand-off command failed", cmd.Process.Release())
	return nil
}

// openArgs returns the program and argv that hand target to goos in the
// requested mode.
func openArgs(goos string, mode openMode, target string) (string, []string) {
	switch mode {
	case openAs:
		switch goos {
		case "darwin":
			return "open", []string{target}
		case "windows":
			return "rundll32", []string{"shell32.dll,OpenAs_RunDLL", target}
		default:
			return "xdg-open", []string{target}
		}
	case reveal:
		switch goos {
		case "darwin":
			return "open", []string{"-R", target}
		case "windows":
			return "explorer", []string{"/select,", target}
		default:
			// No cross-platform "reveal" verb outside the two above:
			// the closest honest answer is the containing directory.
			return "xdg-open", []string{filepath.Dir(target)}
		}
	default:
		switch goos {
		case "darwin":
			return "open", []string{target}
		case "windows":
			return "rundll32", []string{"url.dll,FileProtocolHandler", target}
		default:
			return "xdg-open", []string{target}
		}
	}
}

// openWith hands target to the OS in the requested mode.
func openWith(ctx context.Context, goos string, mode openMode, target string) error {
	program, args := openArgs(goos, mode, target)
	return launch(ctx, program, args)
}
