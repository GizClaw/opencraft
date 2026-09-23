package desktop

import (
	"fmt"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// This file is the native menu bar's table: what the menus contain, what
// each item is called in either language, and which key (if any) it
// advertises. menu.go walks it; menuspec_test.go holds it to its contracts.
//
// A row is one of three kinds:
//
//   - a *role* row, which carries an AppKit selector (cut:, hide:, terminate:,
//     ...). Wails builds the action from the role, so a role row may not also
//     have a click callback — the selector wins and the callback would
//     silently never run.
//   - a *command* row, whose id comes from the frontend's keyboard table
//     (frontend/src/lib/keys.ts). Clicking one emits that id to the webview
//     and the frontend runs it the way it runs a keystroke. These are the rows
//     the app owns, and the only ones this table's rules are about.
//   - a *shell action* row for something Go does itself (closing the window,
//     opening a URL).
//
// # Accelerators
//
// Accelerator strings use Wails' syntax, and a command row's accelerator is
// its keyboard-table combo with "Mod+" written out ("CmdOrCtrl+K") — that
// equality is what menuspec_test.go checks, so the menu bar and the shortcut
// sheet cannot name one key differently.
//
// What an accelerator may spell is narrower than what the sheet may list.
// AppKit matches a key equivalent against the event's
// charactersIgnoringModifiers, which drops every modifier but ⇧ and caps lock —
// so ⇧ turns on whether the key *types* a different character, and the answer
// decides whether a combo exists in the menu bar at all:
//
//   - a ⇧-modified *letter* can never match. The typed character is the
//     capital ("Z" for ⇧⌘Z), while Wails lowercases the key before AppKit ever
//     sees it (pkg/application parseKey) and nothing in its API takes a
//     capital back: the item would advertise a key that does nothing.
//   - a ⇧-modified *punctuation* key matches, because its typed character is
//     still the unshifted one — ⇧⌘[ reports "[" and matches the "[" key
//     equivalent with the shift mask, and the menu draws it as ⇧⌘[. (Handing
//     over the shifted glyph instead — "{" — matches too, but the menu would
//     then *display* that glyph, so the bar and the sheet would spell one key
//     two ways.)
//   - ⌥ is the same as ⇧ for punctuation and harmless for letters, since
//     charactersIgnoringModifiers ignores it either way.
//
// The rows whose frontend binding needs a modifier macOS cannot express
// therefore carry no accelerator at all. The key still works: AppKit has no
// item claiming it, so the event reaches the webview and the frontend
// dispatcher, exactly as on Windows and Linux — and ⌘/ lists it. A menu item
// with no key is honest; one with a dead key is not.
//
// Which leaves the interesting half: for the rows that *do* carry one, the
// native menu consumes the key — the frontend never sees it — and the click
// bridge runs the same command. One implementation, two fronts.
//
// Platform rows (Edit, Window, zoom, full screen) keep the accelerators the
// platform's own menu uses, spelled the way Wails spells them; they are
// mirrored rather than invented — with one exception, because a mirrored key
// that cannot fire is worse than none: Wails' Redo row is spelled
// "CmdOrCtrl+Shift+z", which is exactly the ⇧-letter case above, so this bar
// leaves the key off. menuspec_test.go holds every row, roles included, to
// that rule.
const repositoryURL = "https://github.com/GizClaw/opencraft"

// menuEntry is one row of the menu bar.
type menuEntry struct {
	// role draws and runs the platform's own item. Zero means "not a role".
	role application.Role
	// command is a shortcut id from frontend/src/lib/keys.ts, bridged back
	// to the frontend on click.
	command string
	// action runs something the shell owns, for the rows that are neither a
	// platform role nor a frontend command.
	action func(*Desktop)
	// label resolves the row's title. Nil keeps the platform's own copy
	// (the system-populated Services submenu).
	label func(core.DesktopTexts) string
	// accelerator is Wails' syntax; empty for a row with no key to show.
	accelerator string
	separator   bool
}

// menuDef is one menu in the bar.
type menuDef struct {
	title   func(core.DesktopTexts) string
	entries []menuEntry
}

// labelOf reads a title straight off the menu copy.
func labelOf(pick func(core.MenuTexts) string) func(core.DesktopTexts) string {
	return func(t core.DesktopTexts) string { return pick(t.Menu) }
}

// titleOf is labelOf for the rows that name the product ("Quit OpenCraft").
func titleOf(
	pick func(core.MenuTexts) string,
	name string,
) func(core.DesktopTexts) string {
	return func(t core.DesktopTexts) string {
		return fmt.Sprintf(pick(t.Menu), name)
	}
}

// labelfOf is labelOf for a row whose copy carries a value ("Session %d").
func labelfOf(
	pick func(core.MenuTexts) string,
	args ...any,
) func(core.DesktopTexts) string {
	return func(t core.DesktopTexts) string {
		return fmt.Sprintf(pick(t.Menu), args...)
	}
}

// sessionSlotRow is one of the keyboard's session slots: the digit is the
// row's number in the sidebar (frontend/src/lib/sessionSlots.ts) and its
// accelerator, so the menu names the slot the sidebar numbered.
func sessionSlotRow(n int) menuEntry {
	return commandRow(
		fmt.Sprintf("session.slot%d", n),
		fmt.Sprintf("CmdOrCtrl+%d", n),
		labelfOf(func(m core.MenuTexts) string { return m.SessionSlot }, n),
	)
}

// commandRow builds a bridged row: the id the frontend runs, its key, and
// the copy.
func commandRow(
	command, accelerator string,
	label func(core.DesktopTexts) string,
) menuEntry {
	return menuEntry{command: command, accelerator: accelerator, label: label}
}

// roleRow builds a platform row: the role supplies the action, the label the
// copy, the accelerator the platform's own key.
func roleRow(
	role application.Role,
	label func(core.DesktopTexts) string,
	accelerator string,
) menuEntry {
	return menuEntry{role: role, label: label, accelerator: accelerator}
}

// actionRow builds a row whose action the shell runs itself.
func actionRow(
	label func(core.DesktopTexts) string,
	action func(*Desktop),
) menuEntry {
	return menuEntry{label: label, action: action}
}

func sep() menuEntry { return menuEntry{separator: true} }

// menuBar is the whole bar, in order: the application menu first, Help last.
// `name` is the product name the launcher resolved ("OpenCraft (dev)"), which
// the application menu and its items are named after.
func menuBar(name string) []menuDef {
	return []menuDef{
		{
			// Named after the product, not the menu: it is the title macOS
			// shows bold next to the Apple logo.
			title: func(core.DesktopTexts) string { return name },
			entries: []menuEntry{
				roleRow(
					application.About,
					titleOf(func(m core.MenuTexts) string { return m.AboutFormat }, name),
					"",
				),
				sep(),
				commandRow(
					"settings.open",
					"CmdOrCtrl+,",
					labelOf(func(m core.MenuTexts) string { return m.Settings }),
				),
				sep(),
				// The Services submenu is filled in by the system, and Wails
				// registers it by role: keep the platform's own row rather
				// than relabel a menu whose contents are not ours.
				{role: application.ServicesMenu},
				sep(),
				roleRow(
					application.Hide,
					titleOf(func(m core.MenuTexts) string { return m.HideFormat }, name),
					"CmdOrCtrl+h",
				),
				roleRow(
					application.HideOthers,
					labelOf(func(m core.MenuTexts) string { return m.HideOthers }),
					"CmdOrCtrl+OptionOrAlt+h",
				),
				roleRow(
					application.ShowAll,
					labelOf(func(m core.MenuTexts) string { return m.ShowAll }),
					"",
				),
				sep(),
				roleRow(
					application.Quit,
					titleOf(func(m core.MenuTexts) string { return m.QuitFormat }, name),
					"CmdOrCtrl+q",
				),
			},
		},
		{
			title: labelOf(func(m core.MenuTexts) string { return m.File }),
			entries: []menuEntry{
				commandRow(
					"chat.new",
					"CmdOrCtrl+N",
					labelOf(func(m core.MenuTexts) string { return m.NewChat }),
				),
				sep(),
				// ⌘W closes the window, and a close is not a quit: the window
				// hook in main.go funnels it into hide-or-quit exactly like
				// the traffic lights do.
				{
					label:       labelOf(func(m core.MenuTexts) string { return m.CloseWindow }),
					accelerator: "CmdOrCtrl+w",
					action: func(d *Desktop) {
						if w := d.core.Shell.MainWindow(); w != nil {
							w.Close()
						}
					},
				},
			},
		},
		{
			title: labelOf(func(m core.MenuTexts) string { return m.Edit }),
			entries: []menuEntry{
				roleRow(
					application.Undo,
					labelOf(func(m core.MenuTexts) string { return m.Undo }),
					"CmdOrCtrl+z",
				),
				roleRow(
					application.Redo,
					labelOf(func(m core.MenuTexts) string { return m.Redo }),
					// ⇧⌘Z, which Wails spells "CmdOrCtrl+Shift+z":
					// a shifted letter, and lowercased, so AppKit
					// would never match it. The row still redoes on
					// a click.
					"",
				),
				sep(),
				roleRow(
					application.Cut,
					labelOf(func(m core.MenuTexts) string { return m.Cut }),
					"CmdOrCtrl+x",
				),
				roleRow(
					application.Copy,
					labelOf(func(m core.MenuTexts) string { return m.Copy }),
					"CmdOrCtrl+c",
				),
				roleRow(
					application.Paste,
					labelOf(func(m core.MenuTexts) string { return m.Paste }),
					"CmdOrCtrl+v",
				),
				// Wails' own Delete row carries a bare ⌫ as its key
				// equivalent; a bare key is not worth trusting as a key
				// equivalent, and the row works without one.
				roleRow(
					application.Delete,
					labelOf(func(m core.MenuTexts) string { return m.Delete }),
					"",
				),
				roleRow(
					application.SelectAll,
					labelOf(func(m core.MenuTexts) string { return m.SelectAll }),
					"CmdOrCtrl+a",
				),
			},
		},
		{
			title: labelOf(func(m core.MenuTexts) string { return m.View }),
			entries: []menuEntry{
				commandRow(
					"palette.open",
					"CmdOrCtrl+K",
					labelOf(func(m core.MenuTexts) string { return m.CommandPalette }),
				),
				sep(),
				commandRow(
					"panel.files",
					"CmdOrCtrl+O",
					labelOf(func(m core.MenuTexts) string { return m.FilesPanel }),
				),
				// ⇧⌘G, which macOS cannot take (see the file comment).
				commandRow(
					"panel.git",
					"",
					labelOf(func(m core.MenuTexts) string { return m.GitPanel }),
				),
				sep(),
				// ⇧⌘T, same reason.
				commandRow(
					"theme.cycle",
					"",
					labelOf(func(m core.MenuTexts) string { return m.CycleTheme }),
				),
				sep(),
				roleRow(
					application.ResetZoom,
					labelOf(func(m core.MenuTexts) string { return m.ResetZoom }),
					"CmdOrCtrl+0",
				),
				roleRow(
					application.ZoomIn,
					labelOf(func(m core.MenuTexts) string { return m.ZoomIn }),
					"CmdOrCtrl+plus",
				),
				roleRow(
					application.ZoomOut,
					labelOf(func(m core.MenuTexts) string { return m.ZoomOut }),
					"CmdOrCtrl+-",
				),
				sep(),
				roleRow(
					application.ToggleFullscreen,
					labelOf(func(m core.MenuTexts) string { return m.FullScreen }),
					"Ctrl+Command+F",
				),
				// ⌥⌘I is the platform's devtools key, and an ⌥-modified
				// letter is not expressible either: the row keeps the action
				// without advertising a key. Wails compiles the role out of
				// production builds, so this is a dev-build affordance.
				roleRow(
					application.OpenDevTools,
					labelOf(func(m core.MenuTexts) string { return m.DevTools }),
					"",
				),
			},
		},
		{
			title: labelOf(func(m core.MenuTexts) string { return m.Chat }),
			entries: []menuEntry{
				commandRow(
					"chat.focusComposer",
					"CmdOrCtrl+L",
					labelOf(func(m core.MenuTexts) string { return m.FocusComposer }),
				),
				// ⇧⌘C.
				commandRow(
					"chat.copyLastReply",
					"",
					labelOf(func(m core.MenuTexts) string { return m.CopyLastReply }),
				),
				// Esc stops the turn; ⌘. is the same command for the menu,
				// where a bare Escape would be an odd thing to advertise.
				commandRow(
					"turn.stop",
					"CmdOrCtrl+.",
					labelOf(func(m core.MenuTexts) string { return m.StopReply }),
				),
				sep(),
				sessionSlotRow(1),
				sessionSlotRow(2),
				sessionSlotRow(3),
				sessionSlotRow(4),
				sep(),
				commandRow(
					"session.prev",
					"CmdOrCtrl+[",
					labelOf(func(m core.MenuTexts) string { return m.PrevSession }),
				),
				commandRow(
					"session.next",
					"CmdOrCtrl+]",
					labelOf(func(m core.MenuTexts) string { return m.NextSession }),
				),
				sep(),
				// ⇧⌘[ / ⇧⌘].
				commandRow(
					"workspace.prev",
					"CmdOrCtrl+Shift+[",
					labelOf(func(m core.MenuTexts) string { return m.PrevWorkspace }),
				),
				commandRow(
					"workspace.next",
					"CmdOrCtrl+Shift+]",
					labelOf(func(m core.MenuTexts) string { return m.NextWorkspace }),
				),
			},
		},
		{
			title: labelOf(func(m core.MenuTexts) string { return m.Window }),
			entries: []menuEntry{
				roleRow(
					application.Minimise,
					labelOf(func(m core.MenuTexts) string { return m.Minimise }),
					"CmdOrCtrl+M",
				),
				roleRow(
					application.Zoom,
					labelOf(func(m core.MenuTexts) string { return m.Zoom }),
					"",
				),
				sep(),
				roleRow(
					application.BringAllToFront,
					labelOf(func(m core.MenuTexts) string { return m.BringAllToFront }),
					"",
				),
			},
		},
		{
			title: labelOf(func(m core.MenuTexts) string { return m.Help }),
			entries: []menuEntry{
				// ⌘/ is the sheet's own key, so the row that opens it can
				// carry it: the whole reference is one keystroke from the
				// menu bar.
				commandRow(
					"shortcuts.open",
					"CmdOrCtrl+/",
					labelOf(func(m core.MenuTexts) string { return m.KeyboardShortcuts }),
				),
				sep(),
				actionRow(
					labelOf(func(m core.MenuTexts) string { return m.Repository }),
					func(d *Desktop) { d.core.Shell.OpenURL(repositoryURL) },
				),
			},
		},
	}
}
