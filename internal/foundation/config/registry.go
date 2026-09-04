package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WorkspaceMeta records one previously opened workspace inside the
// workspaces root. This is the canonical data model; adapters may wrap
// it but must not define their own duplicate JSON schema.
type WorkspaceMeta struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	LastOpened string `json:"last_opened"` // RFC3339 UTC
}

// SaveWorkspace persists one workspace open (creating or refreshing
// its meta record).
func SaveWorkspace(dataDir, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config: workspace path is required")
	}
	root, err := WorkspaceRoot(dataDir, path)
	if err != nil {
		return err
	}
	meta := WorkspaceMeta{
		ID:         WorkspaceID(path),
		Path:       filepath.Clean(path),
		Title:      filepath.Base(filepath.Clean(path)),
		LastOpened: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode workspace meta: %w", err)
	}
	return writeFileAtomic(filepath.Join(root, "meta.json"), data, 0o600)
}

// ListWorkspaces returns every previously opened workspace, newest
// first. Missing or unparsable meta files are skipped.
func ListWorkspaces(dataDir string) ([]WorkspaceMeta, error) {
	dir, err := WorkspacesRoot(dataDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []WorkspaceMeta{}, nil
		}
		return nil, err
	}
	var out []WorkspaceMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var meta WorkspaceMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.Path == "" || meta.ID == "" {
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

// RemoveWorkspace removes one workspace's meta/state directory. The
// workspace itself is never touched.
func RemoveWorkspace(dataDir, id string) error {
	id = strings.TrimSpace(id)
	if !IsWorkspaceID(id) {
		return fmt.Errorf("config: invalid workspace id %q", id)
	}
	dir, err := WorkspacesRoot(dataDir)
	if err != nil {
		return err
	}
	entry := filepath.Join(dir, id)
	if _, err := os.Stat(entry); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.RemoveAll(entry); err != nil {
		return fmt.Errorf("config: remove workspace %s: %w", id, err)
	}
	return nil
}

// IsWorkspaceID validates the hex shape of a workspace id.
func IsWorkspaceID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
