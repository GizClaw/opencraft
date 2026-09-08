// Command opencraft is the opencraft desktop application. It runs the
// assembled flowcraft runtime behind a Wails v3 shell: Go services drive
// sessions, agents, and configuration, while the event bridge pushes runtime
// streams (tokens, tool calls, interactions) into the React frontend embedded
// in the binary.
package main

import (
	"embed"
	"log"
	"os"
	"sync/atomic"
	"time"

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

	var quitRequested atomic.Bool
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
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.gizclaw.opencraft",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				shell.Emit("second instance args=%v workingDir=%s", data.Args, data.WorkingDir)
				shell.ShowMainWindow()
			},
		},
		OnShutdown: func() {
			quitRequested.Store(true)
		},
	})

	mainURL := "/"
	if os.Getenv("V3_AUTO") == "1" {
		mainURL = "/#auto"
	}
	mainW := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "OpenCraft",
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        700,
		URL:              mainURL,
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
		BackgroundColour: application.NewRGB(15, 18, 24),
		EnableFileDrop:   true,
	})

	shell = desktop.NewShell(app, d)
	shell.SetMain(mainW)
	app.RegisterService(application.NewService(shell))
	d.RegisterServices(app)

	// Mirror v3 events to the process log for automated runs.
	app.Event.On("v3:log", func(e *application.CustomEvent) {
		log.Printf("V3EVENT sender=%q data=%v", e.Sender, e.Data)
	})

	// Close-to-background: intercept native closes and hide the main window
	// until a real quit is requested.
	mainW.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitRequested.Load() {
			return
		}
		shell.Emit("cancel close; hiding main window")
		e.Cancel()
		mainW.Hide()
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
		shell.Emit("mac ApplicationShouldHandleReopen")
		shell.ShowMainWindow()
	})

	menu := app.NewMenu()
	menu.Add("Show OpenCraft").OnClick(func(*application.Context) {
		shell.ShowMainWindow()
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("OpenCraft")
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
		log.Fatalf("opencraft: %v", err)
	}
}
