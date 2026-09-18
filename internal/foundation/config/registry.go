package config

import (
	"context"
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
			telemetry.WarnErr(context.Background(),
				"config: decode workspace meta failed", err,
				otellog.String("path", filepath.Join(dir, entry.Name())))
			continue
		}
		if meta.Path == "" || meta.ID == "" {
			continue
		}
		out = append(out, meta)
	}
	// Rank by last opened, newest first. The parse happens once per
	// entry here rather than inside the comparator, and ties (equal or
	// unparsable stamps) fall back to title/path: callers render this
	// order verbatim, so it must not shuffle between reloads the way an
	// unstable sort over equal keys would.
	type ranked struct {
		meta WorkspaceMeta
		at   time.Time
	}
	order := make([]ranked, 0, len(out))
	for _, m := range out {
		at, err := time.Parse(time.RFC3339Nano, m.LastOpened)
		if err != nil {
			telemetry.WarnErr(context.Background(),
				"config: parse workspace last opened failed", err,
				otellog.String("workspace.id", m.ID))
		}
		order = append(order, ranked{meta: m, at: at})
	}
	sort.SliceStable(order, func(i, j int) bool {
		if !order[i].at.Equal(order[j].at) {
			return order[i].at.After(order[j].at)
		}
		if order[i].meta.Title != order[j].meta.Title {
			return order[i].meta.Title < order[j].meta.Title
		}
		return order[i].meta.Path < order[j].meta.Path
	})
	rankedOut := make([]WorkspaceMeta, 0, len(order))
	for _, r := range order {
		rankedOut = append(rankedOut, r.meta)
	}
	return rankedOut, nil
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
