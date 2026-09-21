package bindings

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// Workspace exposes workspace history/open/remove operations.
type Workspace struct {
	core *core.Core
}

// NewWorkspace wires the workspace binding.
func NewWorkspace(c *core.Core) *Workspace {
	return &Workspace{core: c}
}

// List returns previously opened workspaces, newest first.
func (b *Workspace) List() ([]config.WorkspaceMeta, error) {
	return b.core.Workspaces()
}

// Active returns the active workspace path.
func (b *Workspace) Active() string {
	return b.core.ActiveWorkDir()
}

// Open acquires a Host for workDir and records it in history.
func (b *Workspace) Open(workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return errors.New("workspace path is required")
	}
	info, err := os.Stat(workDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", workDir)
	}
	ctx := b.core.Shell.Context()
	b.core.SetWorkDir(workDir)
	// Record the open before the rebuild: RebuildRuntime emits "ready"
	// from inside, and the UI reloads workspace history on that event.
	// Writing first keeps the invariant that anything observing a
	// finished open already sees this workspace's new last_opened.
	b.core.RecordWorkspace(workDir)
	ctx = host.WithAssemblyReason(ctx, host.ReasonWorkspaceOpen)
	if err := b.core.RebuildRuntime(ctx); err != nil {
		return err
	}
	return nil
}

// Remove drops one workspace from history and switches to the next
// recent workspace when the active one is removed.
func (b *Workspace) Remove(id string) error {
	next, active, err := b.core.RemoveWorkspace(id)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	if next != "" {
		return b.Open(next)
	}
	ctx := b.core.Shell.Context()
	b.core.SetWorkDir("")
	return b.core.RebuildRuntime(
		host.WithAssemblyReason(ctx, host.ReasonWorkspaceOpen))
}

// ChooseWorkspace opens a native picker and opens the selection.
func (b *Workspace) ChooseWorkspace(
	title string,
) (string, error) {
	path, err := b.core.Shell.OpenDirectoryDialog(
		title, b.core.ActiveWorkDir())
	if err != nil || path == "" {
		return path, err
	}
	if err := b.Open(path); err != nil {
		return "", err
	}
	return path, nil
}
