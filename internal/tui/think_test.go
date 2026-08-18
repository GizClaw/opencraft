package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThinkCommandSetsAndPersists(t *testing.T) {
	dir := t.TempDir()
	m := newTestModel()
	m.opts.ConfigDir = dir

	m.input.SetValue("/think high")
	updated, _ := m.runCommand("think")
	next := updated.(*Model)

	if next.thinkLevel != EffortHigh {
		t.Fatalf("thinkLevel = %q, want high", next.thinkLevel)
	}
	if got := next.transcriptText(); !strings.Contains(got, "think: high") {
		t.Errorf("confirmation missing from transcript: %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tui.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "high") {
		t.Errorf("tui.yaml does not persist high: %s", data)
	}
	// The composer returns to idle with the command consumed.
	if next.input.Value() != "" {
		t.Errorf("composer not reset, still %q", next.input.Value())
	}
}

func TestThinkCommandCycles(t *testing.T) {
	m := newTestModel()
	m.thinkLevel = EffortLow
	m.input.SetValue("/think")
	updated, _ := m.runCommand("think")
	if got := updated.(*Model).thinkLevel; got != EffortMedium {
		t.Errorf("cycle from low = %q, want medium", got)
	}
}

func TestThinkCommandRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	m := newTestModel()
	m.opts.ConfigDir = dir
	m.thinkLevel = EffortMedium

	m.input.SetValue("/think extreme")
	updated, _ := m.runCommand("think")
	next := updated.(*Model)
	if next.thinkLevel != EffortMedium {
		t.Errorf("invalid arg changed level to %q", next.thinkLevel)
	}
	if got := next.transcriptText(); !strings.Contains(got, "用法") {
		t.Errorf("usage hint missing from transcript: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "tui.yaml")); !os.IsNotExist(err) {
		t.Errorf("invalid arg should not persist settings (stat err=%v)", err)
	}
}

func TestThinkLevelLoadedFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := saveSettings(dir, settings{ReasoningEffort: EffortLow}); err != nil {
		t.Fatal(err)
	}
	m := New(nil, Options{ConfigDir: dir}, NewBridge(16), nil)
	if m.thinkLevel != EffortLow {
		t.Errorf("thinkLevel = %q, want low", m.thinkLevel)
	}
}

func TestThinkLevelShownInFooter(t *testing.T) {
	m := newTestModel()
	m.thinkLevel = EffortHigh
	if got := m.footerLine(); !strings.Contains(got, "effort high") {
		t.Errorf("footer missing effort: %q", got)
	}
}
