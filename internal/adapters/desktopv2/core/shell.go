package core

import (
	"context"
	"runtime"
	"sync"

	"github.com/GizClaw/flowcraft/core/telemetry"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Shell owns the native window/tray lifecycle and persisted desktop
// preferences. It is the desktopv2 replacement for the old App
// lifecycle/close/tray fields.
type Shell struct {
	mu sync.Mutex

	ctx           context.Context
	userDir       string
	prefs         DesktopPrefs
	quitting      bool
	quitConfirmed bool
	trayItems     *trayItems
	trayEnd       func()
}

// NewShell creates the shell with preferences loaded from userDir.
func NewShell(userDir string) *Shell {
	prefs := LoadPrefs(userDir)
	return &Shell{
		userDir: userDir,
		prefs:   prefs,
	}
}

// SetContext is called by Startup.
func (s *Shell) SetContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

// Context returns the Wails context installed by Startup, falling back
// to a background context before Startup so callers never need their
// own Background fallback.
func (s *Shell) Context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// OpenURL opens an http(s) URL in the system default browser. Before
// Startup there is no Wails context, so the call is a no-op.
func (s *Shell) OpenURL(url string) {
	ctx := s.activeContext()
	if ctx == nil {
		return
	}
	wailsruntime.BrowserOpenURL(ctx, url)
}

// activeContext returns the real Wails context, or nil before Startup.
func (s *Shell) activeContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

// Emit pushes one UI event to the frontend when a Wails context is
// installed. Events before Startup are dropped.
func (s *Shell) Emit(typ string, data any) {
	ctx := s.activeContext()
	if ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, "opencraft:ui", map[string]any{
		"type": typ,
		"data": data,
	})
}

// CloseRequested is the single funnel for window-close paths.
func (s *Shell) CloseRequested(ctx context.Context) bool {
	s.mu.Lock()
	closeToTray := s.prefs.CloseToTray
	quitting := s.quitting
	quitConfirmed := s.quitConfirmed
	s.mu.Unlock()

	if quitting && quitConfirmed {
		return false
	}
	if quitting || !closeToTray {
		if !s.confirmQuit(ctx) {
			if quitting {
				s.clearQuitRequest()
			}
			return true
		}
		s.mu.Lock()
		s.quitting = true
		s.quitConfirmed = true
		s.mu.Unlock()
		return false
	}

	wailsruntime.Hide(ctx)
	return true
}

func (s *Shell) confirmQuit(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	texts := s.Texts()
	buttons := []string{texts.QuitDialogConfirm, texts.QuitDialogCancel}
	defaultButton := texts.QuitDialogCancel
	cancelButton := texts.QuitDialogCancel
	if runtime.GOOS != "darwin" {
		buttons = []string{"Yes", "No"}
		defaultButton = "No"
		cancelButton = "No"
	}
	selection, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.QuestionDialog,
		Title:         texts.QuitDialogTitle,
		Message:       texts.QuitDialogMessage,
		Buttons:       buttons,
		DefaultButton: defaultButton,
		CancelButton:  cancelButton,
	})
	if err != nil {
		telemetry.WarnErr(ctx, "desktop: exit confirmation dialog failed", err)
		return false
	}
	return selection == texts.QuitDialogConfirm || selection == "Yes"
}

// MarkQuitting records an unconfirmed quit request.
func (s *Shell) MarkQuitting() {
	s.mu.Lock()
	s.quitting = true
	s.quitConfirmed = false
	s.mu.Unlock()
}

func (s *Shell) clearQuitRequest() {
	s.mu.Lock()
	s.quitting = false
	s.quitConfirmed = false
	s.mu.Unlock()
}

// RequestClose mirrors the native close path from JS.
func (s *Shell) RequestClose() {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		return
	}
	if s.CloseRequested(ctx) {
		return
	}
	wailsruntime.Quit(ctx)
}

// ShowMainWindow restores the main window.
func (s *Shell) ShowMainWindow() {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		return
	}
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.WindowShow(ctx)
	wailsruntime.Show(ctx)
}

// QuitFromTray terminates the app from the tray menu.
func (s *Shell) QuitFromTray() {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		return
	}
	s.MarkQuitting()
	wailsruntime.Quit(ctx)
}

// GetCloseToTray reports the persisted close behavior.
func (s *Shell) GetCloseToTray() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs.CloseToTray
}

// SetCloseToTray persists the close behavior.
func (s *Shell) SetCloseToTray(closeToTray bool) error {
	return s.commit(func(p *DesktopPrefs) {
		p.CloseToTray = closeToTray
	})
}

// Language returns the current desktop language.
func (s *Shell) Language() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs.Language
}

// SetLanguage persists the UI language. Tray retitling is wired by the
// desktopv2 root when tray support is added.
func (s *Shell) SetLanguage(language string) error {
	language = NormalizeLanguage(language)
	if err := s.commit(func(p *DesktopPrefs) {
		p.Language = language
	}); err != nil {
		return err
	}
	s.updateTrayTexts()
	return nil
}

// Texts returns the native desktop copy for the current language.
func (s *Shell) Texts() DesktopTexts {
	s.mu.Lock()
	language := s.prefs.Language
	s.mu.Unlock()
	return TextsFor(language)
}

// SessionDefaults returns the mode/think level applied to newly
// minted conversations.
func (s *Shell) SessionDefaults() (mode, think string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs.DefaultMode, s.prefs.DefaultThink
}

// SetSessionDefaults persists the default mode/think level. Values
// must already be canonical.
func (s *Shell) SetSessionDefaults(mode, think string) error {
	return s.commit(func(p *DesktopPrefs) {
		p.DefaultMode = mode
		p.DefaultThink = think
	})
}

// commit applies one mutation to the in-memory preference document and
// writes it back under the same lock. Holding the lock across the write
// serializes setters so concurrent updates cannot overwrite each
// other's fields, and a failed write restores the previous document.
func (s *Shell) commit(mutate func(*DesktopPrefs)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.prefs
	mutate(&s.prefs)
	if err := SavePrefs(s.userDir, s.prefs); err != nil {
		s.prefs = prev
		return err
	}
	return nil
}
