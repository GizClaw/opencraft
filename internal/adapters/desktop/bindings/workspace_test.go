package bindings

import (
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
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

// TestWorkspaceOpenRecordsHistoryBeforeReady pins the ordering the UI
// relies on: the "ready" event emitted by the runtime rebuild makes the
// frontend reload workspace history, so the record has to be on disk by
// the time that event goes out.
func TestWorkspaceOpenRecordsHistoryBeforeReady(t *testing.T) {
	c := core.NewCore(t.TempDir(), t.TempDir(), "")
	b := NewWorkspace(c)
	workDir := t.TempDir()

	// Replacing the pet sink is this package's event-observer hook:
	// Shell.Emit feeds it every UI event, ready included.
	var sawReady, recordedAtReady bool
	c.Shell.SetPetSink(func(typ string, data any) {
		if typ != "ready" {
			return
		}
		sawReady = true
		metas, err := c.Workspaces()
		recordedAtReady = err == nil && len(metas) == 1 &&
			filepath.Clean(metas[0].Path) == filepath.Clean(workDir)
	})

	if err := b.Open(workDir); err != nil {
		t.Fatal(err)
	}
	if !sawReady {
		t.Fatal("open emitted no ready event")
	}
	if !recordedAtReady {
		t.Fatal("workspace history was not recorded before the ready event")
	}
}
