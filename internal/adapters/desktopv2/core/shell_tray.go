package core

import (
	"fmt"
	"runtime"
	"sync/atomic"

	"fyne.io/systray"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/mainthread"
	appversion "github.com/GizClaw/opencraft/internal/foundation/version"
)

type trayItems struct {
	version *systray.MenuItem
	about   *systray.MenuItem
	show    *systray.MenuItem
	quit    *systray.MenuItem
}

var trayStarted atomic.Bool

// StartTray launches the system tray / menu bar icon.
func (s *Shell) StartTray(icon, iconWindows []byte) {
	start, end := systray.RunWithExternalLoop(func() {
		s.trayReady(icon, iconWindows)
	}, func() {
		trayStarted.Store(false)
	})
	s.mu.Lock()
	s.trayEnd = end
	s.mu.Unlock()
	trayStarted.Store(true)
	mainthread.Run(start)
}

func (s *Shell) trayReady(icon, iconWindows []byte) {
	texts := s.Texts()
	if runtime.GOOS == "windows" {
		if iconWindows != nil {
			systray.SetIcon(iconWindows)
		} else if icon != nil {
			systray.SetIcon(icon)
		}
	} else if icon != nil {
		systray.SetIcon(icon)
	}
	systray.SetTooltip(texts.TrayTooltip)

	version := systray.AddMenuItem(
		fmt.Sprintf(texts.VersionFormat, appversion.ServiceVersion),
		texts.VersionTooltip,
	)
	version.Disable()
	about := systray.AddMenuItem(texts.About, texts.VersionTooltip)
	about.Disable()
	systray.AddSeparator()
	show := systray.AddMenuItem(texts.Show, texts.TrayTooltip)
	systray.AddSeparator()
	quit := systray.AddMenuItem(texts.Quit, texts.TrayTooltip)
	s.mu.Lock()
	s.trayItems = &trayItems{version: version, about: about, show: show, quit: quit}
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-show.ClickedCh:
				s.ShowMainWindow()
			case <-quit.ClickedCh:
				s.QuitFromTray()
				return
			}
		}
	}()
}

// StopTray tears the tray down during shutdown.
func (s *Shell) StopTray() {
	if trayStarted.CompareAndSwap(true, false) {
		s.mu.Lock()
		end := s.trayEnd
		s.trayEnd = nil
		s.trayItems = nil
		s.mu.Unlock()
		if end != nil {
			mainthread.Run(end)
		}
	}
}

func (s *Shell) updateTrayTexts() {
	texts := s.Texts()
	s.mu.Lock()
	items := s.trayItems
	s.mu.Unlock()
	if items == nil {
		return
	}
	items.version.SetTitle(fmt.Sprintf(texts.VersionFormat, appversion.ServiceVersion))
	items.version.SetTooltip(texts.VersionTooltip)
	items.about.SetTitle(texts.About)
	items.show.SetTitle(texts.Show)
	items.quit.SetTitle(texts.Quit)
}
