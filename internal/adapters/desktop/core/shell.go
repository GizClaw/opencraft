package core

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Shell owns the v3 application-facing shell: event emission, external URL
// opening, native dialogs and persisted desktop preferences. It is created
// before the Wails application exists and is attached by the desktop entry
// once app + main window are available, so all platform calls are nil-safe.
type Shell struct {
	mu sync.Mutex

	app            *application.App
	main           *application.WebviewWindow
	ctx            context.Context
	userDir        string
	prefs          DesktopPrefs
	dialogIcon     []byte
	quitting       bool
	quitConfirmed  bool
	scheduledTasks func(context.Context) bool
	onLanguage     func()
	notifySink     func(typ string, data any)
}

// NewShell creates the shell with preferences loaded from userDir.
func NewShell(userDir string) *Shell {
	prefs := LoadPrefs(userDir)
	return &Shell{
		userDir: userDir,
		prefs:   prefs,
	}
}

// Attach wires the live Wails v3 application and its main window.
func (s *Shell) Attach(app *application.App, main *application.WebviewWindow) {
	s.mu.Lock()
	s.app = app
	s.main = main
	s.mu.Unlock()
}

// SetDialogIcon supplies the application icon shown by native dialogs such as
// the quit confirmation.
func (s *Shell) SetDialogIcon(icon []byte) {
	s.mu.Lock()
	s.dialogIcon = icon
	s.mu.Unlock()
}

// SetContext installs the application lifecycle context.
func (s *Shell) SetContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

// SetScheduledTasksChecker installs the check the quit funnel uses to
// decide whether exiting would stop scheduled tasks.
func (s *Shell) SetScheduledTasksChecker(
	checker func(context.Context) bool,
) {
	s.mu.Lock()
	s.scheduledTasks = checker
	s.mu.Unlock()
}

// SetLanguageChangedListener wires a refresh hook for native surfaces that
// mirror the persisted language (system tray labels, ...).
func (s *Shell) SetLanguageChangedListener(fn func()) {
	s.mu.Lock()
	s.onLanguage = fn
	s.mu.Unlock()
}

// SetNotificationSink installs an optional observer invoked for every UI
// event right after the frontend emit. The desktop adapter uses it to raise
// native system notifications for interact/turn_end/automation events from
// Go, so hidden or suspended windows never lose notifications.
func (s *Shell) SetNotificationSink(fn func(typ string, data any)) {
	s.mu.Lock()
	s.notifySink = fn
	s.mu.Unlock()
}

// Context returns the installed application context, falling back to a
// background context before startup so callers never handle nil.
func (s *Shell) Context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *Shell) attached() (*application.App, *application.WebviewWindow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app, s.main
}

// OpenURL opens an http(s) URL in the system default browser.
func (s *Shell) OpenURL(url string) {
	app, _ := s.attached()
	if app == nil {
		return
	}
	if err := app.Browser.OpenURL(url); err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: open external url failed", err)
	}
}

// Emit pushes one UI event to the frontend. Events before attachment are
// dropped.
func (s *Shell) Emit(typ string, data any) {
	app, _ := s.attached()
	if app == nil {
		return
	}
	app.Event.Emit("opencraft:ui", map[string]any{
		"type": typ,
		"data": data,
	})
	s.mu.Lock()
	fn := s.notifySink
	s.mu.Unlock()
	if fn != nil {
		fn(typ, data)
	}
}

// ShouldQuit is the synchronous gate for every real quit path (tray, Cmd+Q,
// application menu). It returns true only when quitting may proceed. When the
// scheduled-task check requires confirmation it opens the native v3 dialog
// asynchronously and returns false; the dialog callback retries the quit.
func (s *Shell) ShouldQuit() bool {
	s.mu.Lock()
	quitting := s.quitting
	confirmed := quitting && s.quitConfirmed
	s.mu.Unlock()
	if confirmed {
		return true
	}
	if quitting {
		// A confirmation dialog is already pending; a second quit request
		// must not open another one.
		return false
	}

	app, _ := s.attached()
	if !s.confirmQuitRequired(s.Context()) {
		s.markQuitConfirmed()
		return true
	}
	if app == nil {
		// No native shell yet; do not trap callers during startup/tests.
		s.markQuitConfirmed()
		return true
	}

	s.mu.Lock()
	s.quitting = true
	s.quitConfirmed = false
	s.mu.Unlock()
	s.confirmQuitAsync(app)
	return false
}

func (s *Shell) markQuitConfirmed() {
	s.mu.Lock()
	s.quitting = true
	s.quitConfirmed = true
	s.mu.Unlock()
}

// confirmQuitAsync shows the native question dialog. v3 dialogs are
// asynchronous: the buttons carry the quit/keep-running callbacks.
func (s *Shell) confirmQuitAsync(app *application.App) {
	texts := s.Texts()
	d := app.Dialog.Question().
		SetTitle(texts.QuitDialogTitle).
		SetMessage(texts.QuitDialogMessage)
	s.mu.Lock()
	icon := s.dialogIcon
	s.mu.Unlock()
	if len(icon) > 0 {
		d.SetIcon(icon)
	}
	confirm := d.AddButton(texts.QuitDialogConfirm)
	cancel := d.AddButton(texts.QuitDialogCancel)
	confirm.SetAsDefault()
	cancel.SetAsCancel()
	confirm.OnClick(func() {
		s.markQuitConfirmed()
		app.Quit()
	})
	cancel.OnClick(func() {
		s.clearQuitRequest()
	})
	if _, main := s.attached(); main != nil {
		d.AttachToWindow(main)
	}
	d.Show()
}

// confirmQuitRequired decides whether a real quit needs confirmation.
func (s *Shell) confirmQuitRequired(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	s.mu.Lock()
	checker := s.scheduledTasks
	s.mu.Unlock()
	if checker == nil {
		return true
	}
	return checker(ctx)
}

// QuitRequested reports whether a quit flow has started: either an
// unconfirmed confirmation dialog is pending or the quit was already
// confirmed. Window teardown that happens after this state must not trigger a
// second quit request.
func (s *Shell) QuitRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quitting
}

func (s *Shell) clearQuitRequest() {
	s.mu.Lock()
	s.quitting = false
	s.quitConfirmed = false
	s.mu.Unlock()
}

// CloseRequested is the window-close funnel used by lifecycle bindings.
// It returns true when the close must be cancelled (hide to tray or quit
// dialog pending) and false when the application may terminate.
func (s *Shell) CloseRequested(ctx context.Context) bool {
	s.mu.Lock()
	closeToTray := s.prefs.CloseToTray
	quitting := s.quitting
	quitConfirmed := s.quitConfirmed
	s.mu.Unlock()

	if quitting && quitConfirmed {
		return false
	}
	if quitting {
		// An unconfirmed quit dialog is already pending; cancel this close.
		return true
	}
	if !closeToTray {
		return !s.ShouldQuit()
	}

	_, main := s.attached()
	if main != nil {
		main.Hide()
	}
	return true
}

// RequestClose mirrors the custom title-bar close button.
func (s *Shell) RequestClose() {
	s.mu.Lock()
	closeToTray := s.prefs.CloseToTray
	quitting := s.quitting
	quitConfirmed := s.quitConfirmed
	s.mu.Unlock()

	if quitting {
		if quitConfirmed {
			s.QuitApplication()
		}
		return
	}
	if closeToTray {
		_, main := s.attached()
		if main != nil {
			main.Hide()
		}
		return
	}
	if s.ShouldQuit() {
		s.QuitApplication()
	}
}

// QuitApplication requests shutdown through the v3 lifecycle.
func (s *Shell) QuitApplication() {
	app, _ := s.attached()
	if app != nil {
		app.Quit()
	}
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

// SetLanguage persists the UI language.
func (s *Shell) SetLanguage(language string) error {
	language = NormalizeLanguage(language)
	if err := s.commit(func(p *DesktopPrefs) {
		p.Language = language
	}); err != nil {
		return err
	}
	s.mu.Lock()
	fn := s.onLanguage
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

// Texts returns the native desktop copy for the current language.
func (s *Shell) Texts() DesktopTexts {
	s.mu.Lock()
	language := s.prefs.Language
	s.mu.Unlock()
	return TextsFor(language)
}

// OpenDirectoryDialog opens a native folder picker.
func (s *Shell) OpenDirectoryDialog(title, defaultDirectory string) (string, error) {
	app, main := s.attached()
	if app == nil {
		return "", nil
	}
	d := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                title,
		Directory:            defaultDirectory,
		CanChooseDirectories: true,
		CanChooseFiles:       false,
		CanCreateDirectories: true,
	})
	if main != nil {
		d.AttachToWindow(main)
	}
	return d.PromptForSingleSelection()
}

// OpenFileDialog opens a native file picker with one optional filter pattern.
func (s *Shell) OpenFileDialog(title, defaultDirectory, pattern string) (string, error) {
	app, main := s.attached()
	if app == nil {
		return "", nil
	}
	opts := &application.OpenFileDialogOptions{
		Title:                title,
		Directory:            defaultDirectory,
		CanChooseFiles:       true,
		CanChooseDirectories: false,
	}
	if pattern != "" {
		opts.Filters = []application.FileFilter{{
			DisplayName: "Files",
			Pattern:     pattern,
		}}
	}
	d := app.Dialog.OpenFileWithOptions(opts)
	if main != nil {
		d.AttachToWindow(main)
	}
	return d.PromptForSingleSelection()
}

// SaveFileDialog opens a native save dialog for one file.
func (s *Shell) SaveFileDialog(defaultFilename, defaultDirectory string) (string, error) {
	app, main := s.attached()
	if app == nil {
		return "", nil
	}
	d := app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Filename:             defaultFilename,
		Directory:            defaultDirectory,
		CanCreateDirectories: true,
	})
	if main != nil {
		d.AttachToWindow(main)
	}
	return d.PromptForSingleSelection()
}

// SessionDefaults returns the mode/think level applied to newly minted
// conversations.
func (s *Shell) SessionDefaults() (mode, think string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs.DefaultMode, s.prefs.DefaultThink
}

// SetSessionDefaults persists the default mode/think level. Values must
// already be canonical.
func (s *Shell) SetSessionDefaults(mode, think string) error {
	return s.commit(func(p *DesktopPrefs) {
		p.DefaultMode = mode
		p.DefaultThink = think
	})
}

// commit applies one mutation to the in-memory preference document and
// writes it back under the same lock.
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
