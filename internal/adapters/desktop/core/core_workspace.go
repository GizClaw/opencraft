package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Workspaces lists previously opened workspaces, newest first.
func (c *Core) Workspaces() ([]config.WorkspaceMeta, error) {
	return config.ListWorkspaces(c.DataDir)
}

// SetWorkDir changes the active workspace directory.
func (c *Core) SetWorkDir(workDir string) {
	c.mu.Lock()
	c.WorkDir = workDir
	c.mu.Unlock()
}

// ActiveWorkDir returns the active workspace directory.
func (c *Core) ActiveWorkDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.WorkDir
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
// history.
func (c *Core) InitialWorkDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return startupWorkDirFromHistory(c.DataDir)
}
