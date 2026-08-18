package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// typeKeys feeds runes through the key handler like real typing, so
// the palette re-searches on every keystroke.
func typeKeys(m *Model, s string) {
	for _, r := range s {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestPaletteOpensAndRanks(t *testing.T) {
	m := newTestModel()
	m.width = 80
	typeKeys(m, "/")
	if !m.paletteOpen() {
		t.Fatal("palette should open when input starts with /")
	}
	if len(m.palette.results) != 4 {
		t.Fatalf("empty query should list all commands: %v",
			m.palette.results)
	}
	typeKeys(m, "re")
	if len(m.palette.results) == 0 ||
		m.palette.results[0] != "resume" {
		t.Fatalf("/re should rank resume first: %v", m.palette.results)
	}
	v := m.View()
	if !strings.Contains(v, "? Commands") ||
		!strings.Contains(v, "/resume") {
		t.Errorf("palette not rendered above composer: %q", v)
	}
}

func TestPaletteMatchesDescription(t *testing.T) {
	m := newTestModel()
	m.width = 80
	typeKeys(m, "/sandbox")
	if len(m.palette.results) == 0 ||
		m.palette.results[0] != "permissions" {
		t.Fatalf("/sandbox should find permissions by description: %v",
			m.palette.results)
	}
}

func TestPaletteNavigationAndEnter(t *testing.T) {
	m := newTestModel()
	m.width = 80
	typeKeys(m, "/")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(*Model)
	if next.palette.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", next.palette.cursor)
	}
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if next.mode != modePermissions {
		t.Fatalf("enter should run the highlighted command, mode = %v",
			next.mode)
	}
	if next.palette.open {
		t.Error("palette should close after running a command")
	}
}

func TestPaletteExactCommandRuns(t *testing.T) {
	m := newTestModel()
	typeKeys(m, "/permissions")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if next.mode != modePermissions {
		t.Fatalf("exact /permissions should enter permissions mode, got %v",
			next.mode)
	}
}

func TestPaletteEscCloses(t *testing.T) {
	m := newTestModel()
	typeKeys(m, "/re")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.paletteOpen() {
		t.Error("esc should close the palette")
	}
	if m.input.Value() != "" {
		t.Errorf("esc should clear the input, got %q", m.input.Value())
	}
}

func TestPaletteTabCompletes(t *testing.T) {
	m := newTestModel()
	typeKeys(m, "/pe")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(*Model)
	if m.input.Value() != "/permissions" {
		t.Errorf("tab should complete to /permissions, got %q",
			m.input.Value())
	}
}

func TestPaletteNoMatchSubmitsAsMessage(t *testing.T) {
	m := newTestModel()
	typeKeys(m, "/usr/bin")
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if next.mode != modeRunning {
		t.Fatalf("no-match enter should submit as a normal message, mode = %v",
			next.mode)
	}
	if cmd == nil {
		t.Fatal("submit should return a start-turn command")
	}
}
