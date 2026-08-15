package tui

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/tui/commands"
)

// TestSlashHandlersMatchCorpus guards the two command registries from
// drifting apart: the corpus in the commands subpackage (which the
// palette searches and completes against) and slashHandlers in
// dispatch.go (which actually executes). A command listed without a
// handler would run silently; a handler without a corpus entry would
// be unreachable.
func TestSlashHandlersMatchCorpus(t *testing.T) {
	listed := commands.List()
	if len(listed) != len(slashHandlers) {
		t.Fatalf("commands.List() = %d, slashHandlers = %d; "+
			"keep both registries in sync", len(listed), len(slashHandlers))
	}
	for _, c := range listed {
		if slashHandlers[c.Name] == nil {
			t.Errorf("command %q has no handler in slashHandlers", c.Name)
		}
	}
	for name := range slashHandlers {
		if _, ok := commands.Lookup(name); !ok {
			t.Errorf("handler %q is missing from the command corpus", name)
		}
	}
}

// TestRunCommandUnknownSurfacesError checks the defensive path: a name
// outside the corpus must not silently no-op.
func TestRunCommandUnknownSurfacesError(t *testing.T) {
	m := newTestModel()
	updated, cmd := m.runCommand("definitely-not-a-command")
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("unknown command should flush a pending message")
	}
	if len(next.stream.pending) != 0 {
		t.Errorf("pending should be drained by the flush command, got %v",
			next.stream.pending)
	}
}
