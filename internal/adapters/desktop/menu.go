package desktop

import (
	"runtime"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// desktopMenu is the menu bar once it is built: the rows that carry app copy,
// so a language change can relabel them, plus the composition root their
// commands bridge back to.
type desktopMenu struct {
	desktop *Desktop
	labels  []menuLabel
}

// menuLabel is one row that shows app copy. Rows the platform labels itself
// (the Services submenu, whose contents are not ours) are not in here.
type menuLabel struct {
	item  *application.MenuItem
	label func(core.DesktopTexts) string
}

// SetupMenu installs the application menu bar. It runs after application.New
// and before app.Run: Wails records the menu and applies it as the run loop
// starts.
//
// macOS only. On Windows and Linux the app draws its own top bar and its
// window is frameless, so a native menu would be a second one — and on Linux
// it would be a GTK menu bar drawn inside that frameless window. Those
// platforms keep the frontend dispatcher as the only owner of keys.
func (d *Desktop) SetupMenu(app *application.App) {
	if runtime.GOOS != "darwin" || app == nil {
		return
	}
	texts := d.core.Shell.Texts()
	menu := &desktopMenu{desktop: d}
	root := application.NewMenu()
	for _, def := range menuBar(d.appTooltip(texts.TrayTooltip)) {
		sub := root.AddSubmenu(def.title(texts))
		for _, entry := range def.entries {
			menu.add(sub, entry, texts)
		}
	}
	app.Menu.Set(root)
	// The menu bar mirrors the UI language, like the tray's labels.
	d.core.Shell.AddLanguageChangedListener(menu.refresh)
}

// add appends one row to a menu.
func (m *desktopMenu) add(
	parent *application.Menu,
	entry menuEntry,
	texts core.DesktopTexts,
) {
	switch {
	case entry.separator:
		parent.AddSeparator()
		return
	case entry.role != application.NoRole && entry.label == nil:
		// The platform's own row, copy and all: the Services submenu is
		// populated by the system and registered by role.
		parent.AddRole(entry.role)
		return
	}
	item := parent.Add(entry.label(texts))
	m.labels = append(m.labels, menuLabel{item: item, label: entry.label})
	if entry.role != application.NoRole {
		// A role's action is an AppKit selector, so a role row must not also
		// carry a callback: the selector wins and the callback never runs.
		item.SetRole(entry.role)
	}
	if entry.accelerator != "" {
		item.SetAccelerator(entry.accelerator)
	}
	if entry.command != "" {
		command := entry.command
		item.OnClick(func(*application.Context) {
			m.desktop.core.Shell.EmitMenuCommand(command)
		})
	}
	if entry.action != nil {
		action := entry.action
		item.OnClick(func(*application.Context) { action(m.desktop) })
	}
}

// refresh copies the current locale into the menu bar.
func (m *desktopMenu) refresh() {
	if m == nil || m.desktop == nil {
		return
	}
	texts := m.desktop.core.Shell.Texts()
	for _, row := range m.labels {
		row.item.SetLabel(row.label(texts))
	}
}
