// Command opencraft is the opencraft desktop application. It runs the
// assembled flowcraft runtime behind a Wails shell: Go bindings drive
// sessions, agents, and configuration, while the event bridge pushes
// runtime streams (tokens, tool calls, interactions) into the React
// frontend embedded in the binary.
package main

import (
	"embed"
	"log"
	"os"

	"github.com/GizClaw/opencraft/internal/desktop"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// execd is the internal self-forked sandbox child (see
	// execd_main.go). It must be handled before any GUI machinery
	// starts: the desktop process forks itself with `opencraft execd`
	// when a sandboxed command needs an isolated process server.
	if len(os.Args) > 1 && os.Args[1] == "execd" {
		runExecServer()
		return
	}

	app, err := desktop.New(desktop.Options{})
	if err != nil {
		log.Fatalf("opencraft: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     "OpenCraft",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 700,
		// Frameless on every platform: the window has no system title
		// bar or native window buttons. The sidebar renders its own
		// close/minimise/maximise controls and the top strip drags the
		// window, so the app looks identical on macOS and Linux.
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 18, B: 24, A: 1},
		OnStartup:        app.Startup,
		OnShutdown:       app.Shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatalf("opencraft: %v", err)
	}
}
