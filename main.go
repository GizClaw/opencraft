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

	"github.com/GizClaw/opencraft/internal/desktop"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
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
		// Hidden, inset title bar on macOS: the system frame (rounded
		// corners, shadow, traffic lights) stays native while the
		// content extends to the top; the traffic lights are nudged
		// into alignment with the chat header by
		// applyOpenCraftWindowStyle.
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 18, B: 24, A: 1},
		OnStartup: func(ctx context.Context) {
			app.Startup(ctx)
			applyOpenCraftWindowStyle()
		},
		OnShutdown:       app.Shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatalf("opencraft: %v", err)
	}
}
