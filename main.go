// Command opencraft is the opencraft desktop application. It runs the
// assembled flowcraft runtime behind a Wails shell: Go bindings drive
// sessions, agents, and configuration, while the event bridge pushes
// runtime streams (tokens, tool calls, interactions) into the React
// frontend embedded in the binary.
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"runtime"

	"github.com/GizClaw/opencraft/internal/desktop"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

//go:embed build/tray-icon.png
var trayIconTemplate []byte

func main() {
	// execd is the internal self-forked sandbox child (see
	// execd_main.go). It must be handled before any GUI machinery
	// starts: the desktop process forks itself with `opencraft execd`
	// when a sandboxed command needs an isolated process server.
	if len(os.Args) > 1 && os.Args[1] == "execd" {
		runExecServer()
		return
	}

	app, err := desktop.New(desktop.Options{
		// TrayIcon feeds the system tray icon on Windows/Linux (and the
		// fallback on macOS); TrayIconTemplate is the monochrome macOS
		// menu bar glyph, which adapts to light and dark menu bars.
		TrayIcon:         appIcon,
		TrayIconTemplate: trayIconTemplate,
	})
	if err != nil {
		log.Fatalf("opencraft: %v", err)
	}

	opts := &options.App{
		Title:     "OpenCraft",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 18, B: 24, A: 1},
		OnStartup: func(ctx context.Context) {
			app.Startup(ctx)
			applyOpenCraftWindowStyle()
			installOpenCraftReopenHandler()
		},
		OnShutdown: app.Shutdown,
		// Close-to-background: every close path (native window close,
		// the custom title-bar X button, Cmd+Q / Dock Quit on macOS)
		// funnels through OnBeforeClose. It consults the persisted
		// "close to tray" setting: hide the app (macOS hides the whole
		// app, Windows/Linux hide the window) or let Wails quit.
		OnBeforeClose: func(ctx context.Context) bool {
			return app.CloseRequested(ctx)
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			// Stable reverse-DNS id; do not version it. It feeds the
			// Windows mutex, the macOS lock file + distributed
			// notification, and the Linux D-Bus name.
			UniqueId: "com.gizclaw.opencraft",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				// Launcher/Dock/`open -a` while the app is already
				// running: restore the main window instead of starting
				// a second process.
				app.ShowMainWindow()
			},
		},
		Bind: []interface{}{
			app,
		},
	}
	switch runtime.GOOS {
	case "darwin":
		// Hidden, inset title bar on macOS: the system frame (rounded
		// corners, shadow, traffic lights) stays native while the
		// content extends to the top; the traffic lights are nudged
		// into alignment with the chat header by
		// applyOpenCraftWindowStyle. Frameless must stay off here —
		// Wails would strip the traffic lights entirely.
		opts.Mac = &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		}
	case "windows", "linux":
		// Frameless + custom title bar on Windows/Linux to match the
		// macOS look: the webview renders the brand strip and window
		// controls, and Wails' built-in CSS drag/edge-resize handles
		// the window chrome. macOS keeps its native traffic lights.
		opts.Frameless = true
		switch runtime.GOOS {
		case "windows":
			// Keep the Windows 11 rounded corners and Aero shadow;
			// the theme follows the system (WebView2 UI chrome).
			opts.Windows = &windows.Options{
				Theme: windows.SystemDefault,
			}
		case "linux":
			opts.Linux = &linux.Options{
				ProgramName:      "OpenCraft",
				WebviewGpuPolicy: linux.WebviewGpuPolicyAlways,
			}
		}
	}
	err = wails.Run(opts)
	if err != nil {
		log.Fatalf("opencraft: %v", err)
	}
}
