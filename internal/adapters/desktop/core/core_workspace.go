package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// Workspaces lists previously opened workspaces, newest first.
func (c *Core) Workspaces() ([]config.WorkspaceMeta, error) {
	return config.ListWorkspaces(c.DataDir)
}

// SetWorkDir changes the active workspace directory. The path is stored
// in the one spelling config defines for workspaces
// (config.CanonicalWorkDir): the ready event announces this string and
// the window compares it, character for character, against the history
// row the user clicked — so whatever a launch flag or a picker handed in
// has to be folded to that spelling here, or the window takes the row
// for another workspace and a click on it becomes a switch the backend
// answers with "already announced".
func (c *Core) SetWorkDir(workDir string) {
	canonical := config.CanonicalWorkDir(workDir)
	c.mu.Lock()
	c.WorkDir = canonical
	c.mu.Unlock()
}

// ActiveWorkDir returns the active workspace directory.
func (c *Core) ActiveWorkDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.WorkDir
}

// ActiveHost returns the Host serving the window's active workspace, or
// nil when that workspace has none: never assembled, or already torn
// down. It is the one way an adapter asks "where do the reads for what
// the UI shows go"; the pool answers per workspace, so a background
// acquire for another workspace cannot redirect them.
func (c *Core) ActiveHost() *host.Host {
	return c.Runtime.HostFor(c.ActiveWorkDir())
}

// BusyWorkspaces lists the workspaces with a live run, the window's own
// and every other one: the git panel is repository-wide, and a run in a
// sibling workspace of the same repository edits the same files.
func (c *Core) BusyWorkspaces() []string {
	return c.Runtime.BusyWorkspaces()
}

// RecordWorkspace persists one workspace open. Failures are best-effort.
func (c *Core) RecordWorkspace(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	telemetry.WarnErr(context.Background(),
		"desktop: record workspace failed",
		config.SaveWorkspace(c.DataDir, path))
}

// RemoveWorkspace removes one workspace from history. If it is the
// active workspace, it falls back to the next most recent one.
func (c *Core) RemoveWorkspace(id string) (next string, active bool, err error) {
	id = strings.TrimSpace(id)
	if id == "" || !config.IsWorkspaceID(id) {
		return "", false, errors.New("invalid workspace id")
	}
	c.mu.Lock()
	current := c.WorkDir
	c.mu.Unlock()
	if current == "" || config.WorkspaceID(current) != id {
		return "", false, config.RemoveWorkspace(c.DataDir, id)
	}
	next, _, err = c.nextWorkspaceAfterRemoval(id)
	if err != nil {
		return "", false, err
	}
	if err := config.RemoveWorkspace(c.DataDir, id); err != nil {
		return "", false, err
	}
	return next, true, nil
}

func (c *Core) nextWorkspaceAfterRemoval(
	removeID string,
) (string, bool, error) {
	metas, err := c.Workspaces()
	if err != nil {
		return "", false, err
	}
	for _, m := range metas {
		if m.ID == removeID {
			continue
		}
		if info, statErr := os.Stat(m.Path); statErr == nil && info.IsDir() {
			return m.Path, true, nil
		}
	}
	return "", false, nil
}

// ResolveLayout builds and ensures the workspace layout for workDir.
func (c *Core) ResolveLayout(workDir string) (config.WorkspaceLayout, error) {
	layout, err := config.ResolveWorkspace(c.DataDir, workDir)
	if err != nil {
		return layout, err
	}
	telemetry.WarnErr(context.Background(),
		"desktop: ensure workspace layout failed", layout.Ensure())
	return layout, nil
}

// startupWorkDirFromHistory returns the most recent existing workspace,
// skipping home.
func startupWorkDirFromHistory(dataDir string) string {
	metas, err := config.ListWorkspaces(dataDir)
	if err != nil {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: resolve user home failed", err)
	}
	for _, m := range metas {
		if err == nil && filepath.Clean(m.Path) == filepath.Clean(home) {
			continue
		}
		if info, err := os.Stat(m.Path); err == nil && info.IsDir() {
			return m.Path
		}
	}
	return ""
}

// InitialWorkDir resolves the startup workspace from explicit input or
// history, in the one spelling a workspace is written in: this value is
// announced through the ready event and compared against the history
// rows, so it is folded here rather than at whichever caller remembers.
// A row recorded before the rule existed — a relative spelling — is
// stat'd as it stands, which resolves it against the process's working
// directory (the only thing a relative row can be resolved against), and
// answered in the canonical spelling. A relative row that does not
// resolve there is skipped like any other row that no longer exists.
func (c *Core) InitialWorkDir(explicit string) string {
	if explicit != "" {
		return config.CanonicalWorkDir(explicit)
	}
	return config.CanonicalWorkDir(startupWorkDirFromHistory(c.DataDir))
}
