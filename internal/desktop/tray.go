package desktop

import (
	"runtime"
	"sync/atomic"

	"fyne.io/systray"
)

// tray labels. Keep them short: Windows overflow menus truncate long
// labels, and macOS menu bar items should fit in one glance.
const (
	trayTooltip  = "OpenCraft"
	trayShowItem = "打开 OpenCraft"
	trayQuitItem = "退出"
)

// trayStarted guards systray.Quit so Shutdown never touches the systray
// library before Run or after the tray goroutine ended.
var trayStarted atomic.Bool

// startTray launches the system tray / menu bar icon. It must be called
// after Startup has the Wails context: tray menu actions call back into
// the runtime, which needs ctx.
//
// Wails owns the application event loop, so the tray must use fyne's
// external-loop integration instead of systray.Run: on macOS Run() would
// replace Wails' NSApplication delegate and start a second [NSApp run],
// which never fires applicationDidFinishLaunching again and therefore
// never creates the status bar item. RunWithExternalLoop lets the host
// toolkit call start() once the app is running and end() at shutdown.
// The start (status item creation) must run on the main thread on
// macOS, which runOnMain dispatches; Windows/Linux start their own
// loops from any goroutine.
func (a *App) startTray() {
	start, end := systray.RunWithExternalLoop(a.trayReady, func() {
		trayStarted.Store(false)
	})
	a.mu.Lock()
	a.trayEnd = end
	a.mu.Unlock()
	trayStarted.Store(true)
	runOnMain(start)
}

// trayReady runs once the tray exists (after start() on the main
// thread). It installs the icon and menu; callbacks run on the tray
// library's own goroutines and marshal back through the Wails runtime.
func (a *App) trayReady() {
	if runtime.GOOS == "darwin" {
		// Template images adapt to light/dark menu bars; the colour
		// channel is ignored and the alpha is used. Prefer the dedicated
		// monochrome glyph (build/tray-icon.png); fall back to the app
		// icon's silhouette.
		if a.trayIconTemplate != nil {
			systray.SetTemplateIcon(a.trayIconTemplate, a.trayIcon)
		} else if a.trayIcon != nil {
			systray.SetTemplateIcon(a.trayIcon, a.trayIcon)
		}
	} else if a.trayIcon != nil {
		systray.SetIcon(a.trayIcon)
	}
	systray.SetTooltip(trayTooltip)

	show := systray.AddMenuItem(trayShowItem, "Show OpenCraft")
	systray.AddSeparator()
	quit := systray.AddMenuItem(trayQuitItem, "Quit OpenCraft")

	go func() {
		for {
			select {
			case <-show.ClickedCh:
				a.ShowMainWindow()
			case <-quit.ClickedCh:
				a.QuitFromTray()
				return
			}
		}
	}()
}

// stopTray tears the tray down during application shutdown. It is
// best-effort: process termination cleans up anything that remains.
func (a *App) stopTray() {
	if trayStarted.CompareAndSwap(true, false) {
		a.mu.Lock()
		end := a.trayEnd
		a.trayEnd = nil
		a.mu.Unlock()
		if end != nil {
			runOnMain(end)
		}
	}
}
