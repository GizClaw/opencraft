package desktop

import (
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

	"github.com/GizClaw/opencraft/internal/config"
)

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
	_ = saveWorkspaceMeta(dir, path)
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

// RemoveWorkspace drops one workspace from the history (the workspace
// itself is untouched). The id must be a stable hex workspace id.
func (a *App) RemoveWorkspace(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || !isWorkspaceID(id) {
		return errors.New("invalid workspace id")
	}
	dir, err := workspaceHistoryDir()
	if err != nil {
		return err
	}
	return removeWorkspaceMeta(dir, id)
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
