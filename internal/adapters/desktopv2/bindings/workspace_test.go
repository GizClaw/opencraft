package bindings

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestWorkspaceOpenSwitchesActiveDir(t *testing.T) {
	workDir := t.TempDir()
	c := core.NewCore(t.TempDir(), t.TempDir(), "")
	b := NewWorkspace(c)
	if err := b.Open(workDir); err != nil {
		t.Fatal(err)
	}
	if got := c.ActiveWorkDir(); got != workDir {
		t.Fatalf("active work dir = %q, want %q", got, workDir)
	}
}

func TestRemoveActiveWorkspaceReturnsToPickerState(t *testing.T) {
	workDir := t.TempDir()
	c := core.NewCore(t.TempDir(), t.TempDir(), "")
	b := NewWorkspace(c)
	if err := b.Open(workDir); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(config.WorkspaceID(workDir)); err != nil {
		t.Fatal(err)
	}
	if got := c.ActiveWorkDir(); got != "" {
		t.Fatalf("active work dir after removal = %q, want empty", got)
	}
}
