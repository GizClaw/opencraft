// Command opencraft is the opencraft desktop application. It runs the
// assembled flowcraft runtime behind a Wails v3 shell: Go services drive
// sessions, agents, and configuration, while the event bridge pushes runtime
// streams (tokens, tool calls, interactions) into the React frontend embedded
// in the binary.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/opencraft/internal/adapters/desktop"
	"github.com/GizClaw/opencraft/internal/adapters/headless"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

func main() {
	// execd is the internal self-forked sandbox child. It must be handled
	// before any GUI machinery starts.
	if len(os.Args) > 1 && os.Args[1] == "execd" {
		runExecServer()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "run" {
		os.Exit(headless.Main(os.Args[2:]))
	}

	d, err := desktop.New(desktop.Options{
		UserDir: os.Getenv("V3_USER_DIR"),
		DataDir: os.Getenv("V3_DATA_DIR"),
	})
	if err != nil {
		log.Fatalf("opencraft: %v", err)
	}
	d.SetDialogIcon(trayIcon)

	var shell *desktop.Shell

	app := application.New(application.Options{
		Name:        "OpenCraft",
		Description: "A local-first work partner built on flowcraft",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Linux: application.LinuxOptions{
			ProgramName: "OpenCraft",
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.GizClaw.opencraft",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				telemetry.Info(context.Background(),
					fmt.Sprintf("desktop: second instance args=%v workingDir=%s",
						data.Args, data.WorkingDir))
				desktop.ShowMainWindow(shell)
			},
		},
		ShouldQuit: func() bool {
			return d.QuitAllowed()
		},
	})

	mainW := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "OpenCraft",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 700,
		URL:       "/",
		Mac:       application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
		// Windows/Linux keep the custom in-app title bar (frontend TopBar):
		// frameless only there, never on macOS (native traffic lights).
		Frameless: runtime.GOOS != "darwin",
		Windows: application.WindowsWindow{
			Theme: application.SystemDefault,
		},
		Linux: application.LinuxWindow{
			WebviewGpuPolicy: application.WebviewGpuPolicyAlways,
		},
		BackgroundColour: application.NewRGB(15, 18, 24),
		EnableFileDrop:   true,
	})

	shell = desktop.NewShell(app, d)
	desktop.SetMainWindow(shell, mainW)
	app.RegisterService(application.NewService(shell))
	d.RegisterServices(app)

	// Close-to-background: every native close funnels through the same gate as
	// an in-app close. With "close to tray" enabled the close is cancelled
	// and the window hides; otherwise the close becomes a real quit request
	// that still runs the confirmation flow. Once a quit flow already owns
	// the shutdown (tray Quit, Cmd+Q, UI quit), window teardown must not
	// issue a second quit request.
	mainW.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		quitInFlight := d.QuitRequested()
		if d.CloseRequested() {
			e.Cancel()
			return
		}
		if quitInFlight {
			return
		}
		d.RequestQuit()
	})

	// v3 routes drops to the Go window event; forward paths into the shared
	// UI bus so the chat attachment flow keeps its existing shape.
	mainW.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		files := e.Context().DroppedFiles()
		if len(files) == 0 {
			return
		}
		d.EmitUI("files_dropped", files)
	})

	// Dock reopen uses first-class mac events.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		telemetry.Info(context.Background(), "desktop: mac application reopened")
		desktop.ShowMainWindow(shell)
	})

	d.SetupTray(app, trayIcon,
		func() { desktop.ShowMainWindow(shell) },
		func() { d.RequestQuit() },
	)

	// macOS polish (traffic-light alignment, scroll elasticity) after the
	// first page load; no-op on Windows/Linux.
	registerOpenCraftWindowStyleRefresh(mainW)

	if err := app.Run(); err != nil {
		log.Fatalf("opencraft: %v", err)
	}
}
