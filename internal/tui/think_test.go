package tui

import (
	"strings"
	"testing"

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

func TestThinkCommandCycles(t *testing.T) {
	m := newTestModel()
	m.thinkLevel = string(ocsessions.ThinkLow)
	m.input.SetValue("/think")
	updated, _ := m.runCommand("think")
	if got := updated.(*Model).thinkLevel; got != string(ocsessions.ThinkMedium) {
		t.Errorf("cycle from low = %q, want medium", got)
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
