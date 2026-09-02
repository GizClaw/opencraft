package desktop

import (
	"fmt"
	"runtime"
	"sync/atomic"

	"fyne.io/systray"

	app "github.com/GizClaw/opencraft/internal/app"
)

// trayItems holds the live tray menu rows so the language binding can
// retitle them without rebuilding the whole menu.
type trayItems struct {
	version *systray.MenuItem
	about   *systray.MenuItem
	show    *systray.MenuItem
	quit    *systray.MenuItem
}

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
	texts := a.desktopTexts()
	if runtime.GOOS == "windows" {
		if a.trayIconWindows != nil {
			systray.SetIcon(a.trayIconWindows)
		} else if a.trayIcon != nil {
			systray.SetIcon(a.trayIcon)
		}
	} else if a.trayIcon != nil {
		systray.SetIcon(a.trayIcon)
	}
	systray.SetTooltip(texts.trayTooltip)

	version := systray.AddMenuItem(
		fmt.Sprintf(texts.versionFormat, app.ServiceVersion), texts.versionTooltip)
	version.Disable()
	about := systray.AddMenuItem(texts.about, texts.aboutTooltip)
	about.Disable()
	systray.AddSeparator()

	show := systray.AddMenuItem(texts.show, texts.showTooltip)
	systray.AddSeparator()
	quit := systray.AddMenuItem(texts.quit, texts.quitTooltip)
	a.mu.Lock()
	a.trayItems = &trayItems{
		version: version,
		about:   about,
		show:    show,
		quit:    quit,
	}
	a.mu.Unlock()

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

// updateTrayTexts retitles existing tray menu rows after the frontend
// reports a language change. No-op before trayReady has stored rows.
func (a *App) updateTrayTexts() {
	texts := a.desktopTexts()
	a.mu.Lock()
	items := a.trayItems
	a.mu.Unlock()
	if items == nil {
		return
	}
	items.version.SetTitle(fmt.Sprintf(texts.versionFormat, app.ServiceVersion))
	items.version.SetTooltip(texts.versionTooltip)
	items.about.SetTitle(texts.about)
	items.about.SetTooltip(texts.aboutTooltip)
	items.show.SetTitle(texts.show)
	items.show.SetTooltip(texts.showTooltip)
	items.quit.SetTitle(texts.quit)
	items.quit.SetTooltip(texts.quitTooltip)
}

// stopTray tears the tray down during application shutdown. It is
// best-effort: process termination cleans up anything that remains.
func (a *App) stopTray() {
	if trayStarted.CompareAndSwap(true, false) {
		a.mu.Lock()
		end := a.trayEnd
		a.trayEnd = nil
		a.trayItems = nil
		a.mu.Unlock()
		if end != nil {
			runOnMain(end)
		}
	}
}
