package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func TestThinkCommandSetsAndPersists(t *testing.T) {
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := newTestModel()
	m.opts.Sessions = store
	m.opts.ContextID = "s-test"

	m.input.SetValue("/think high")
	updated, _ := m.runCommand("think")
	next := updated.(*Model)

	if next.thinkLevel != string(ocsessions.ThinkHigh) {
		t.Fatalf("thinkLevel = %q, want high", next.thinkLevel)
	}
	if got := next.transcriptText(); !strings.Contains(got, "think: high") {
		t.Errorf("confirmation missing from transcript: %q", got)
	}
	level, err := store.Think("s-test")
	if err != nil {
		t.Fatal(err)
	}
	if level != ocsessions.ThinkHigh {
		t.Errorf("stored level = %q, want high", level)
	}
	// The composer returns to idle with the command consumed.
	if next.input.Value() != "" {
		t.Errorf("composer not reset, still %q", next.input.Value())
	}
}

func TestThinkCommandOpensPicker(t *testing.T) {
	m := newTestModel()
	m.thinkLevel = string(ocsessions.ThinkLow)
	m.input.SetValue("/think")
	updated, cmd := m.runCommand("think")
	if cmd != nil {
		t.Fatalf("opening the picker should not return a command, got %v", cmd)
	}
	next := updated.(*Model)
	if next.mode != modeThink {
		t.Fatalf("mode = %v, want think picker", next.mode)
	}
	// The cursor lands on the currently active level.
	if next.think.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (low)", next.think.cursor)
	}
	if v := next.View(); !strings.Contains(v, "low") || !strings.Contains(v, "high") {
		t.Errorf("picker = %q", v)
	}
	// Esc returns to idle without changing the level.
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(*Model)
	if next.mode != modeIdle {
		t.Fatalf("mode = %v after esc, want idle", next.mode)
	}
	if next.thinkLevel != string(ocsessions.ThinkLow) {
		t.Errorf("thinkLevel changed to %q after cancel", next.thinkLevel)
	}
}

func TestThinkPickerAppliesSelection(t *testing.T) {
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := newTestModel()
	m.opts.Sessions = store
	m.opts.ContextID = "s-test"
	m.thinkLevel = string(ocsessions.ThinkMedium)

	// /think opens the picker at the active level (medium -> index 1).
	m.input.SetValue("/think")
	updated, _ := m.runCommand("think")
	m = updated.(*Model)
	if m.think.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (medium)", m.think.cursor)
	}
	// Down moves to high; Enter applies and returns to idle.
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if m.mode != modeIdle {
		t.Fatalf("mode = %v, want idle after apply", m.mode)
	}
	if m.thinkLevel != string(ocsessions.ThinkHigh) {
		t.Fatalf("thinkLevel = %q, want high", m.thinkLevel)
	}
	if got := m.transcriptText(); !strings.Contains(got, "think: high") {
		t.Errorf("confirmation missing from transcript: %q", got)
	}
	if level, _ := store.Think("s-test"); level != ocsessions.ThinkHigh {
		t.Errorf("stored level = %q, want high", level)
	}
}

func TestThinkPickerUpWraps(t *testing.T) {
	m := newTestModel()
	m.thinkLevel = string(ocsessions.ThinkLow)
	m.input.SetValue("/think")
	updated, _ := m.runCommand("think")
	m = updated.(*Model)
	// Up from low wraps to high.
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(*Model)
	if m.think.cursor != 2 {
		t.Errorf("cursor = %d after up, want 2 (high)", m.think.cursor)
	}
}

func TestThinkCommandRejectsInvalid(t *testing.T) {
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := newTestModel()
	m.opts.Sessions = store
	m.opts.ContextID = "s-test"
	m.thinkLevel = string(ocsessions.ThinkMedium)

	m.input.SetValue("/think extreme")
	updated, _ := m.runCommand("think")
	next := updated.(*Model)
	if next.thinkLevel != string(ocsessions.ThinkMedium) {
		t.Errorf("invalid arg changed level to %q", next.thinkLevel)
	}
	if got := next.transcriptText(); !strings.Contains(got, "用法") {
		t.Errorf("usage hint missing from transcript: %q", got)
	}
	if level, _ := store.Think("s-test"); level != ocsessions.ThinkMedium {
		t.Errorf("invalid arg should keep the stored default, got %q", level)
	}
}

func TestThinkLevelLoadedPerSession(t *testing.T) {
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetThink("s-a", ocsessions.ThinkHigh); err != nil {
		t.Fatal(err)
	}
	m := New(nil, Options{
		ContextID: "s-a",
		Sessions:  store,
	}, NewBridge(16), nil)
	if m.thinkLevel != string(ocsessions.ThinkHigh) {
		t.Errorf("thinkLevel = %q, want low", m.thinkLevel)
	}
}

func TestThinkLevelShownInFooter(t *testing.T) {
	m := newTestModel()
	m.thinkLevel = string(ocsessions.ThinkHigh)
	if got := m.footerLine(); !strings.Contains(got, "effort high") {
		t.Errorf("footer missing effort: %q", got)
	}
}
