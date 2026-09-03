package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// ProjectConfigStatus is retained as a compatibility view. Project
// configuration layers are gone; the status is always not present.
type ProjectConfigStatus struct {
	Present bool   `json:"present"`
	Trusted bool   `json:"trusted"`
	Path    string `json:"path,omitempty"`
}

// WorkspaceMeta is the canonical workspace history record owned by
// foundation/config.
type WorkspaceMeta = config.WorkspaceMeta

// workspaceHistoryDir returns the workspaces root under the user data
// directory. Tests use it to construct legacy-style roots; App methods
// should prefer the dataDir-aware helpers below.
func workspaceHistoryDir() (string, error) {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return "", err
	}
	return config.WorkspacesRoot(dataDir)
}

// workspaceID derives the canonical stable id for a workspace path.
func workspaceID(path string) string {
	return config.WorkspaceID(path)
}

// saveWorkspaceMeta persists one workspace open under root. root is
// expected to be <dataDir>/workspaces, matching config.SaveWorkspace.
func saveWorkspaceMeta(root, path string) error {
	return config.SaveWorkspace(filepath.Dir(root), path)
}

// loadWorkspaces lists history under root, newest first.
func loadWorkspaces(root string) ([]WorkspaceMeta, error) {
	return config.ListWorkspaces(filepath.Dir(root))
}

// removeWorkspaceMeta removes one workspace state directory under root.
func removeWorkspaceMeta(root, id string) error {
	return config.RemoveWorkspace(filepath.Dir(root), id)
}

// recordWorkspace persists one workspace open. Failures are
// best-effort: a broken history record must never block switching.
func (a *App) recordWorkspace(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	dataDir := a.dataDir
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return
		}
	}
	if err := config.SaveWorkspace(dataDir, path); err != nil {
		telemetry.Warn(a.appContext(),
			"desktop: save workspace history failed",
			otellog.String("path", path),
			otellog.String("error", err.Error()))
	}
}

// Workspaces returns the previously opened workspaces, most recently
// opened first.
func (a *App) Workspaces() ([]WorkspaceMeta, error) {
	dataDir := a.dataDir
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return nil, err
		}
	}
	return config.ListWorkspaces(dataDir)
}

// startupWorkDir resolves the initial workspace: an explicitly passed
// directory wins, otherwise the most recently opened workspace from
// history is restored.
func startupWorkDir(explicit, historyDir string) string {
	if explicit != "" {
		return explicit
	}
	if historyDir == "" {
		return ""
	}
	return lastWorkspaceFromDir(historyDir)
}

// lastWorkspaceFromDir picks the most recent still-existing workspace,
// skipping the user's home.
func lastWorkspaceFromDir(dir string) string {
	metas, err := loadWorkspaces(dir)
	if err != nil || len(metas) == 0 {
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

// RemoveWorkspace drops one workspace from history (the workspace
// itself is untouched).
func (a *App) RemoveWorkspace(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || !isWorkspaceID(id) {
		return errors.New("invalid workspace id")
	}
	dir, err := a.workspaceHistoryDir()
	if err != nil {
		return err
	}
	current := a.snapshotWorkDir()
	if current == "" || workspaceID(current) != id {
		return removeWorkspaceMeta(dir, id)
	}
	next, ok, err := nextWorkspaceAfterRemoval(dir, id)
	if err != nil {
		return err
	}
	if err := removeWorkspaceMeta(dir, id); err != nil {
		return err
	}
	if ok {
		return a.OpenWorkspace(next)
	}
	return a.closeWorkspace()
}

// workspaceHistoryDir returns the workspaces root under the App's data
// directory.
func (a *App) workspaceHistoryDir() (string, error) {
	dataDir := a.dataDir
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return "", err
		}
	}
	return config.WorkspacesRoot(dataDir)
}

// nextWorkspaceAfterRemoval returns the most recently opened existing
// workspace other than removeID.
func nextWorkspaceAfterRemoval(
	dir, removeID string,
) (string, bool, error) {
	metas, err := loadWorkspaces(dir)
	if err != nil {
		return "", false, err
	}
	for _, m := range metas {
		if m.ID == removeID {
			continue
		}
		if info, err := os.Stat(m.Path); err == nil && info.IsDir() {
			return m.Path, true, nil
		}
	}
	return "", false, nil
}

// isWorkspaceID validates the hex shape of a workspace id.
func isWorkspaceID(id string) bool {
	return config.IsWorkspaceID(id)
}

// ProjectConfigStatus reports project-layer compatibility status.
func (a *App) ProjectConfigStatus() ProjectConfigStatus {
	return ProjectConfigStatus{}
}

// SetProjectTrust is retained for API compatibility and is a no-op:
// project config layers no longer exist.
func (a *App) SetProjectTrust(dir string, trusted bool) error {
	return nil
}
