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
// the runtime, which needs ctx. systray.Run blocks, so it runs on its
// own goroutine (Wails owns the main OS thread on every platform).
func (a *App) startTray() {
	go systray.Run(
		func() {
			if a.trayIcon != nil {
				systray.SetIcon(a.trayIcon)
				if runtime.GOOS == "darwin" {
					// Template images adapt to light/dark menu bars; the
					// colour channel is ignored and the alpha is used.
					systray.SetTemplateIcon(a.trayIcon, a.trayIcon)
				}
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
		},
		func() {
			trayStarted.Store(false)
		},
	)
	trayStarted.Store(true)
}

// stopTray tears the tray down during application shutdown. It is
// best-effort: process termination cleans up anything that remains.
func (a *App) stopTray() {
	if trayStarted.CompareAndSwap(true, false) {
		systray.Quit()
	}
}
