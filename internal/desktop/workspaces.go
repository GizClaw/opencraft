package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/config"
)

// ProjectConfigStatus describes whether the current workspace carries a
// project configuration layer and whether the user has trusted it.
// Untrusted project layers are skipped by the config loader, so a
// third-party repo cannot silently override hooks, sandbox policy, or
// the execution graph.
type ProjectConfigStatus struct {
	Present bool   `json:"present"`
	Trusted bool   `json:"trusted"`
	Path    string `json:"path,omitempty"`
}

const trustFileName = "trust.json"

type projectTrustRecord struct {
	Trusted   bool   `json:"trusted"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// WorkspaceMeta describes one previously opened workspace for the
// sidebar history list.
type WorkspaceMeta struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	LastOpened string `json:"last_opened"` // RFC3339 UTC
}

// workspaceHistoryDir returns ~/.opencraft/workspaces, creating it if
// needed. Each workspace gets its own directory named by a stable id
// hash, so future workspace-scoped data can live alongside the meta
// record.
func workspaceHistoryDir() (string, error) {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// workspaceID derives a stable, path-safe id from a workspace path.
func workspaceID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:16])
}

// saveWorkspaceMeta persists one workspace open under root (creating
// or refreshing its meta record).
func saveWorkspaceMeta(root, path string) error {
	id := workspaceID(path)
	meta := WorkspaceMeta{
		ID:         id,
		Path:       path,
		Title:      filepath.Base(path),
		LastOpened: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	entry := filepath.Join(root, id)
	if err := os.MkdirAll(entry, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(entry, "meta.json"), data, 0o600)
}

// recordWorkspace persists one workspace open (creating or refreshing
// its meta record). Failures are best-effort: a broken history record
// must never block switching workspaces.
func (a *App) recordWorkspace(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	dir, err := workspaceHistoryDir()
	if err != nil {
		return
	}
	if err := saveWorkspaceMeta(dir, path); err != nil {
		telemetry.Warn(context.Background(),
			"desktop: save workspace history failed",
			otellog.String("path", path),
			otellog.String("error", err.Error()))
	}
}

// Workspaces returns the previously opened workspaces, most recently
// opened first. The list is unbounded — the UI decides how many to
// show in the sidebar and how many in the full history dialog.
func (a *App) Workspaces() ([]WorkspaceMeta, error) {
	dir, err := workspaceHistoryDir()
	if err != nil {
		return nil, err
	}
	return loadWorkspaces(dir)
}

func loadWorkspaces(dir string) ([]WorkspaceMeta, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []WorkspaceMeta{}, nil
		}
		return nil, err
	}
	var out []WorkspaceMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var meta WorkspaceMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.Path == "" {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, out[i].LastOpened)
		tj, _ := time.Parse(time.RFC3339Nano, out[j].LastOpened)
		return ti.After(tj)
	})
	return out, nil
}

// startupWorkDir resolves the initial workspace: an explicitly passed
// directory wins, otherwise the most recently opened workspace from
// history is restored. It never adopts the process cwd — macOS Finder
// launches with cwd "/" and Windows Explorer with the exe folder, so
// either would silently pick an unintended workspace — and never the
// user's home. A fresh install (empty history) therefore starts with
// no workspace and the UI shows the welcome screen / workspace picker
// on every platform alike.
func startupWorkDir(explicit, historyDir string) string {
	if explicit != "" {
		return explicit
	}
	if historyDir == "" {
		return ""
	}
	return lastWorkspaceFromDir(historyDir)
}

// lastWorkspaceFromDir picks the most recent still-existing workspace
// from a history directory, skipping the user's home (the old
// Finder-launch fallback, not an intentional project root). Empty
// means no workspace to restore.
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

// RemoveWorkspace drops one workspace from the history (the workspace
// itself is untouched). Removing the currently open workspace closes it
// too: the app switches to the most recent remaining workspace, or
// returns to the no-workspace welcome state when none is left. The id
// must be a stable hex workspace id.
func (a *App) RemoveWorkspace(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || !isWorkspaceID(id) {
		return errors.New("invalid workspace id")
	}
	dir, err := workspaceHistoryDir()
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

// nextWorkspaceAfterRemoval returns the most recently opened existing
// workspace other than removeID. Stale history entries whose directory
// no longer exists are skipped.
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

// ProjectConfigStatus reports whether a project configuration layer
// exists for the current workspace and whether the user has trusted it.
func (a *App) ProjectConfigStatus() ProjectConfigStatus {
	wd := a.snapshotWorkDir()
	if strings.TrimSpace(wd) == "" {
		return ProjectConfigStatus{}
	}
	dir, present := config.ProjectConfigDir(wd)
	if !present {
		return ProjectConfigStatus{}
	}
	return ProjectConfigStatus{
		Present: true,
		Trusted: a.isProjectTrusted(wd),
		Path:    dir,
	}
}

// SetProjectTrust persists the trust decision for one workspace and
// rebuilds the runtime when it applies to the current workspace, so an
// accepted project layer takes effect immediately.
func (a *App) SetProjectTrust(dir string, trusted bool) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("workspace path is required")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if err := writeProjectTrust(dir, trusted); err != nil {
		return err
	}
	wd := a.snapshotWorkDir()
	if filepath.Clean(wd) == filepath.Clean(dir) {
		if err := a.requestRebuild(); err != nil {
			return err
		}
	}
	return nil
}

// isProjectTrusted reads the persisted trust flag for one workspace
// path. Absent or unparsable records default to untrusted.
func (a *App) isProjectTrusted(path string) bool {
	p, err := projectTrustPath(path)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var rec projectTrustRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return false
	}
	return rec.Trusted
}

// projectTrustPath resolves the trust record for a workspace path.
func projectTrustPath(path string) (string, error) {
	dir, err := workspaceHistoryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceID(path), trustFileName), nil
}

// writeProjectTrust persists the trust record atomically under the
// per-workspace history entry (0700).
func writeProjectTrust(path string, trusted bool) error {
	dir, err := workspaceHistoryDir()
	if err != nil {
		return err
	}
	entry := filepath.Join(dir, workspaceID(path))
	if err := os.MkdirAll(entry, 0o700); err != nil {
		return err
	}
	rec := projectTrustRecord{
		Trusted:   trusted,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(entry, ".trust-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(entry, trustFileName))
}

func removeWorkspaceMeta(dir, id string) error {
	entry := filepath.Join(dir, id)
	if _, err := os.Stat(entry); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // idempotent
		}
		return err
	}
	if err := os.RemoveAll(entry); err != nil {
		return fmt.Errorf("remove workspace history: %w", err)
	}
	return nil
}

// isWorkspaceID validates the hex shape of a workspace id so history
// removal can never escape the workspaces directory.
func isWorkspaceID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
