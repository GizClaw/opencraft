//go:build wails3

// Command opencraft (Wails v3 migration skeleton) mirrors the v2 entry in
// main.go so both lines can coexist on the migration branch. It proves the
// v3 shell: application/window lifecycle, services, events, tray, single
// instance and Dock reopen. Domain bindings arrive in Phase 2; until then the
// v3 UI is the minimal frontend-v3 shell.
package main

import (
	"embed"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/GizClaw/opencraft/internal/adapters/desktop"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend-v3/dist
var v3Assets embed.FS

//go:embed build/appicon.png
var v3TrayIcon []byte

func main() {
	var quitRequested atomic.Bool
	var shell *desktop.Shell

	app := application.New(application.Options{
		Name:        "OpenCraft",
		Description: "OpenCraft desktop (Wails v3 migration skeleton)",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(v3Assets),
		},
		Mac: application.MacOptions{
			// Close-to-tray: the app keeps running with a hidden main window
			// until the user explicitly quits.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.gizclaw.opencraft",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				shell.Emit("second instance args=%v workingDir=%s", data.Args, data.WorkingDir)
				shell.ShowMainWindow()
			},
		},
		ShouldQuit: func() bool {
			log.Println("v3 ShouldQuit called")
			return true
		},
		OnShutdown: func() {
			quitRequested.Store(true)
			log.Println("v3 OnShutdown ran")
		},
	})

	shell = desktop.NewShell(app)
	app.RegisterService(application.NewService(shell))

	mainURL := "/"
	if os.Getenv("V3_AUTO") == "1" {
		mainURL = "/#auto"
	}
	mainW := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "OpenCraft (v3 skeleton)",
		Width:            1280,
		Height:           820,
		MinWidth:         900,
		MinHeight:        600,
		URL:              mainURL,
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
		BackgroundColour: application.NewRGB(15, 18, 24),
		EnableFileDrop:   true,
	})
	shell.SetMain(mainW)

	// Mirror v3 events to the process log so automated runs can assert that
	// the frontend received broadcasts and that JS->Go retains the sender.
	app.Event.On("v3:log", func(e *application.CustomEvent) {
		log.Printf("V3EVENT sender=%q data=%v", e.Sender, e.Data)
	})

	// Close-to-tray hook: intercept before the default close listener.
	mainW.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitRequested.Load() {
			return
		}
		shell.Emit("cancel close; hiding main window")
		e.Cancel()
		mainW.Hide()
	})

	// Dock reopen uses first-class mac events instead of the v2 objc observer.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		shell.Emit("mac ApplicationShouldHandleReopen")
		shell.ShowMainWindow()
	})

	// Tray: Show/Quit semantics like the v2 shell_tray implementation.
	menu := app.NewMenu()
	menu.Add("Show OpenCraft").OnClick(func(*application.Context) {
		shell.ShowMainWindow()
	})
	menu.Add("Broadcast test event").OnClick(func(*application.Context) {
		shell.Broadcast("from tray menu")
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray := app.SystemTray.New()
	tray.SetIcon(v3TrayIcon)
	tray.SetTooltip("OpenCraft (v3 skeleton)")
	tray.SetMenu(menu)

	if os.Getenv("V3_AUTO") == "1" {
		time.AfterFunc(1200*time.Millisecond, func() {
			shell.Emit("auto app ready")
		})
		time.AfterFunc(3*time.Second, func() {
			shell.Emit("auto: closing main to exercise close-to-tray")
			mainW.Close()
			time.AfterFunc(900*time.Millisecond, func() {
				shell.Emit("auto: restoring main; visible=%v", mainW.IsVisible())
				shell.ShowMainWindow()
			})
		})
	}

	if err := app.Run(); err != nil {
		log.Fatalf("opencraft-v3: %v", err)
	}
}
