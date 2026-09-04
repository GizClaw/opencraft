package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

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
	c.publishWorkspaceEnv()
}

// publishWorkspaceEnv keeps the process and capability-plugin
// environment aligned with the active workspace before any plugin
// subprocess starts. engine.BuildRuntime publishes the same variables
// during assembly; publishing here also covers plugin UI invocations
// that happen before the first Host is assembled.
func (c *Core) publishWorkspaceEnv() {
	workDir := c.ActiveWorkDir()
	envKeys := []string{
		"OPEN_CRAFT_WORKDIR",
		"OPEN_CRAFT_CACHE",
		"OPEN_CRAFT_DATA_DIR",
		"OPEN_CRAFT_WORKSPACE_DIR",
		"OPEN_CRAFT_SESSIONS_DIR",
		"OPEN_CRAFT_APPROVALS",
		"OPEN_CRAFT_TOOL_CACHE",
		"OPEN_CRAFT_AUDIT_DIR",
	}
	if strings.TrimSpace(workDir) == "" {
		for _, key := range envKeys {
			_ = os.Unsetenv(key)
		}
		if c.Plugin != nil && c.Plugin.Capability != nil {
			c.Plugin.Capability.StopAll()
		}
		return
	}
	layout, err := config.ResolveWorkspace(c.DataDir, workDir)
	if err != nil {
		for _, key := range envKeys {
			_ = os.Unsetenv(key)
		}
		return
	}
	_ = layout.Ensure()
	cacheDir := filepath.Join(c.DataDir, "cache")
	env := []string{
		"OPEN_CRAFT_WORKDIR=" + workDir,
		"OPEN_CRAFT_CACHE=" + cacheDir,
		"OPEN_CRAFT_DATA_DIR=" + c.DataDir,
		"OPEN_CRAFT_WORKSPACE_DIR=" + layout.Root,
		"OPEN_CRAFT_SESSIONS_DIR=" + layout.SessionsDir,
		"OPEN_CRAFT_APPROVALS=" + layout.ApprovalsFile,
		"OPEN_CRAFT_TOOL_CACHE=" + layout.CacheDir,
		"OPEN_CRAFT_AUDIT_DIR=" + layout.AuditDir,
	}
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		_ = os.Setenv(key, value)
	}
	if c.Plugin != nil && c.Plugin.Capability != nil {
		c.Plugin.Capability.SetEnv(env)
		c.Plugin.Capability.StopAll()
	}
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
	_ = config.SaveWorkspace(c.DataDir, path)
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
	_ = layout.Ensure()
	return layout, nil
}

// startupWorkDirFromHistory returns the most recent existing workspace,
// skipping home.
func startupWorkDirFromHistory(dataDir string) string {
	metas, err := config.ListWorkspaces(dataDir)
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	for _, m := range metas {
		if home != "" && filepath.Clean(m.Path) == filepath.Clean(home) {
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
