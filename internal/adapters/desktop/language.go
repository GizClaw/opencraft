package desktop

// desktopTexts returns the native desktop copy for the current
// language. It is safe to call before SetLanguage has ever run; the
// language is seeded from prefs or the process locale in New.
func (a *App) desktopTexts() desktopTexts {
	a.mu.Lock()
	language := a.language
	a.mu.Unlock()
	return desktopTextsFor(language)
}

// SetLanguage is the JS binding the frontend calls when its detected
// language changes. It retitles the live tray menu and persists the
// choice so the next launch opens in the same language.
func (a *App) SetLanguage(language string) error {
	language = normalizeDesktopLanguage(language)
	a.mu.Lock()
	changed := a.language != language
	a.language = language
	a.mu.Unlock()
	if changed {
		a.updateTrayTexts()
	}
	return a.persistDesktopPrefs()
}

// persistDesktopPrefs writes the current desktop preferences without
// dropping fields saved by other bindings.
func (a *App) persistDesktopPrefs() error {
	a.mu.Lock()
	prefs := desktopPrefs{
		CloseToTray: a.closeToTray,
		Language:    a.language,
	}
	a.mu.Unlock()
	return savePrefs(a.userDir, prefs)
}
