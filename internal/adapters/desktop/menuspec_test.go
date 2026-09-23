package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The menu bar and the frontend keyboard table are two lists of the same
// keys, written in two languages: frontend/src/lib/keys.ts is what the
// dispatcher, the palette badges and the shortcut sheet read, and
// menuspec.go is what macOS puts in the menu bar. Neither can import the
// other, so this test reads the TypeScript table and holds the Go table to
// it — a key that exists on one side only is a bug that no runtime check
// would catch, because the side that is missing simply never fires.

// frontendSpec is one row of the frontend's keyboard table.
type frontendSpec struct {
	combos []string
	// editable is true when every combo may fire while a text field has
	// the caret.
	editable bool
	// fieldCombos is the list form: the combos that may, when the spec
	// allows only some of them there.
	fieldCombos []string
}

// fieldCombo reports whether the spec allows this accelerator's combo while a
// text field has the caret — either the whole spec is editable, or the combo
// is in the list form.
func fieldCombo(spec frontendSpec, accelerator string) bool {
	for _, combo := range spec.fieldCombos {
		if acceleratorOf(combo) == accelerator {
			return true
		}
	}
	return false
}

// acceleratorOf spells one frontend combo the way Wails does: the platform
// modifier is "Mod" in the table and "CmdOrCtrl" in an accelerator.
func acceleratorOf(combo string) string {
	return strings.Replace(combo, "Mod+", "CmdOrCtrl+", 1)
}

// frontendShortcuts reads the SHORTCUTS array out of the frontend table. The
// parse is deliberately shallow: entries are objects separated by "  },", and
// within one entry the id, the combos and the editable flag are single lines.
func frontendShortcuts(t *testing.T) map[string]frontendSpec {
	t.Helper()
	path := filepath.Join(
		"..", "..", "..", "frontend", "src", "lib", "keys.ts",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the frontend keyboard table: %v", err)
	}
	body := string(data)
	start := strings.Index(body, "export const SHORTCUTS")
	end := strings.Index(body, "export const KEY_REFERENCES")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("%s: SHORTCUTS / KEY_REFERENCES not found; "+
			"this test reads that file, so keep both markers", path)
	}
	entries := strings.Split(body[start:end], "\n  },")
	idRe := regexp.MustCompile(`id: '([^']+)'`)
	// The combos array is one line, and a combo can itself be a bracket
	// (['Mod+[']), so the greedy match has to run to the last bracket on
	// the line rather than to the first one.
	combosRe := regexp.MustCompile(`(?m)^\s*combos: \[(.*)\],\s*$`)
	// Same shape for the list form of the editable flag, which narrows
	// the rule to the combos a text field may keep.
	editableListRe := regexp.MustCompile(`(?m)^\s*editable: \[(.*)\],\s*$`)
	quoteRe := regexp.MustCompile(`'([^']*)'`)

	specs := map[string]frontendSpec{}
	for _, entry := range entries {
		id := idRe.FindStringSubmatch(entry)
		combos := combosRe.FindStringSubmatch(entry)
		if id == nil || combos == nil {
			continue
		}
		var list []string
		for _, match := range quoteRe.FindAllStringSubmatch(combos[1], -1) {
			list = append(list, match[1])
		}
		var fieldCombos []string
		if m := editableListRe.FindStringSubmatch(entry); m != nil {
			for _, match := range quoteRe.FindAllStringSubmatch(m[1], -1) {
				fieldCombos = append(fieldCombos, match[1])
			}
		}
		specs[id[1]] = frontendSpec{
			combos:      list,
			editable:    strings.Contains(entry, "editable: true"),
			fieldCombos: fieldCombos,
		}
	}
	// Every entry carries an id, so the two counts have to agree: a parse
	// that silently skipped entries would quietly stop guarding them.
	want := strings.Count(body[start:end], "\n    id: '")
	if len(specs) != want {
		t.Fatalf("parsed %d of %d shortcuts out of %s; the table moved",
			len(specs), want, path)
	}
	return specs
}

// rows flattens the menu bar into the rows this test has opinions about.
func rows() []menuEntry {
	var out []menuEntry
	for _, def := range menuBar("OpenCraft") {
		out = append(out, def.entries...)
	}
	return out
}

// TestMenuCommandsExistInTheFrontendTable is the drift guard: every bridged
// row names a shortcut the frontend actually binds.
func TestMenuCommandsExistInTheFrontendTable(t *testing.T) {
	specs := frontendShortcuts(t)
	seen := map[string]bool{}
	for _, entry := range rows() {
		if entry.command == "" {
			continue
		}
		if seen[entry.command] {
			t.Errorf("command %q appears twice in the menu bar", entry.command)
		}
		seen[entry.command] = true
		if _, ok := specs[entry.command]; !ok {
			t.Errorf("menu command %q is not in frontend/src/lib/keys.ts",
				entry.command)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no bridged rows in the menu bar")
	}
}

// TestMenuAcceleratorsMatchTheFrontendTable keeps the two spellings of a key
// together: the menu bar may only advertise a combo the table also binds, and
// only for a row that fires while a text field has the caret — a menu key
// equivalent is delivered whether or not the caret is in a field, so the two
// have to agree about that or the menu would steal a field's own key.
func TestMenuAcceleratorsMatchTheFrontendTable(t *testing.T) {
	specs := frontendShortcuts(t)
	bindable := 0
	for _, entry := range rows() {
		if entry.command == "" || entry.accelerator == "" {
			continue
		}
		spec, ok := specs[entry.command]
		if !ok {
			continue // reported by the drift guard above
		}
		found := false
		for _, combo := range spec.combos {
			if acceleratorOf(combo) == entry.accelerator {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %q advertises %q, which keys.ts does not bind "+
				"(it binds %v)", entry.command, entry.accelerator, spec.combos)
			continue
		}
		if !spec.editable && !fieldCombo(spec, entry.accelerator) {
			t.Errorf("command %q advertises %q in the menu bar but keys.ts "+
				"keeps that combo out of text fields; a menu key "+
				"equivalent is delivered there too", entry.command,
				entry.accelerator)
		}
		bindable++
	}
	if bindable == 0 {
		t.Fatal("no accelerator in the menu bar matches the frontend table")
	}
}

// TestMenuAcceleratorsAreMatchable guards the rule in menuspec.go: AppKit
// matches a key equivalent against the event's charactersIgnoringModifiers,
// which drops every modifier but ⇧ and caps lock. ⇧ on a letter therefore
// types the capital ("Z") while Wails lowercases the key, so such a row can
// never fire; ⇧ on a punctuation key reports the same character either way and
// matches, which is why the workspace rows may spell ⇧⌘[.
//
// Platform rows are checked too: a mirrored spelling that cannot fire is
// exactly what this rule exists to catch, and it is why Redo carries no key.
func TestMenuAcceleratorsAreMatchable(t *testing.T) {
	for _, entry := range rows() {
		if entry.accelerator == "" ||
			!strings.Contains(entry.accelerator, "Shift+") {
			continue
		}
		key := entry.accelerator[strings.LastIndex(entry.accelerator, "+")+1:]
		if len(key) == 1 && isLetterOrDigit(key[0]) {
			t.Errorf("%s advertises %q: AppKit matches the typed character "+
				"and Wails lowercases the key, so a shifted letter or digit "+
				"can never match", rowName(entry), entry.accelerator)
		}
	}
}

// rowName names a row for a test message.
func rowName(entry menuEntry) string {
	if entry.command != "" {
		return entry.command
	}
	return fmt.Sprintf("role:%d", entry.role)
}

// isLetterOrDigit reports whether b is an ASCII letter or digit — the keys
// whose shifted form is a different character rather than the same one.
func isLetterOrDigit(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// TestMenuAcceleratorsAreUnique keeps a key from being claimed twice: the
// first matching item would win and the other would look broken.
func TestMenuAcceleratorsAreUnique(t *testing.T) {
	owner := map[string]string{}
	for _, entry := range rows() {
		if entry.accelerator == "" {
			continue
		}
		id := entry.command
		if id == "" {
			id = fmt.Sprintf("role:%d", entry.role)
		}
		if other, ok := owner[entry.accelerator]; ok {
			t.Errorf("accelerator %q is claimed by both %s and %s",
				entry.accelerator, other, id)
			continue
		}
		owner[entry.accelerator] = id
	}
}

// TestMenuRowsAreWellFormed checks the shape the builder relies on: a row is
// exactly one of the three kinds, and everything that is drawn has copy.
func TestMenuRowsAreWellFormed(t *testing.T) {
	for _, def := range menuBar("OpenCraft") {
		if def.title == nil {
			t.Error("a menu in the bar has no title")
		}
		for _, entry := range def.entries {
			kinds := 0
			if entry.separator {
				kinds++
			}
			if entry.role != application.NoRole {
				kinds++
			}
			if entry.command != "" {
				kinds++
			}
			if entry.action != nil {
				kinds++
			}
			if kinds != 1 {
				t.Errorf("row %+v is %d of separator/role/command/action, want 1",
					entry, kinds)
			}
			if entry.separator {
				continue
			}
			// Only the system-populated Services submenu may go without
			// app copy.
			if entry.label == nil && entry.role != application.ServicesMenu {
				t.Errorf("row %+v has no label", entry)
			}
		}
	}
}

// TestMenuCopyIsLocalized renders every row in both languages: a missing
// field would show up as an empty menu title or item.
func TestMenuCopyIsLocalized(t *testing.T) {
	for _, language := range []string{"zh", "en"} {
		texts := core.TextsFor(language)
		for _, def := range menuBar("OpenCraft (dev)") {
			if title := def.title(texts); strings.TrimSpace(title) == "" {
				t.Errorf("%s: a menu in the bar has an empty title", language)
			}
			for _, entry := range def.entries {
				if entry.label == nil {
					continue
				}
				label := entry.label(texts)
				if strings.TrimSpace(label) == "" {
					t.Errorf("%s: a menu row came out empty", language)
				}
			}
		}
	}
	// The rows that name the product read the resolved name, not a literal.
	zh := core.TextsFor("zh")
	appMenu := menuBar("OpenCraft (dev)")[0]
	quit := appMenu.entries[len(appMenu.entries)-1]
	if got := quit.label(zh); !strings.Contains(got, "OpenCraft (dev)") {
		t.Errorf("quit item = %q, want the resolved product name", got)
	}
}
