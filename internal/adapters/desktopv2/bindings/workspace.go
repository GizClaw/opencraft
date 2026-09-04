package bindings

import (
	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	ctx := b.core.Shell.Context()
	_, err := b.core.Runtime.Acquire(ctx, workDir, interact.Auto{})
	if err != nil {
		return err
	}
	b.core.SetWorkDir(workDir)
	b.core.RecordWorkspace(workDir)
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
	b.core.SetWorkDir("")
	b.core.Runtime.Close()
	return nil
}

// ChooseWorkspace opens a native picker and opens the selection.
func (b *Workspace) ChooseWorkspace(
	title string,
) (string, error) {
	path, err := wailsruntime.OpenDirectoryDialog(
		b.core.Shell.Context(),
		wailsruntime.OpenDialogOptions{
			Title:            title,
			DefaultDirectory: b.core.ActiveWorkDir(),
		},
	)
	if err != nil || path == "" {
		return path, err
	}
	if err := b.Open(path); err != nil {
		return "", err
	}
	return path, nil
}
