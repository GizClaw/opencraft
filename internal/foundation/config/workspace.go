package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceID derives a stable, path-safe id from a workspace path.
// The id is a prefix of the SHA-256 of the cleaned absolute path, so
// two different paths never collide in practice while the id stays
// short enough for file names and URLs.
func WorkspaceID(workDir string) string {
	cleaned := filepath.Clean(workDir)
	abs, err := filepath.Abs(cleaned)
	if err == nil {
		cleaned = abs
	}
	sum := sha256.Sum256([]byte(cleaned))
	return hex.EncodeToString(sum[:16])
}

// WorkspacesRoot returns ~/.opencraft/workspaces (or the equivalent
// under dataDir), creating it when needed.
func WorkspacesRoot(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("config: data dir is required")
	}
	dir := filepath.Join(dataDir, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: create workspaces root: %w", err)
	}
	return dir, nil
}

// WorkspaceRoot returns the state/config directory for one workspace:
// <dataDir>/workspaces/<id>.
func WorkspaceRoot(dataDir, workDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("config: data dir is required")
	}
	if strings.TrimSpace(workDir) == "" {
		return "", fmt.Errorf("config: workspace path is required")
	}
	root := filepath.Join(dataDir, "workspaces", WorkspaceID(workDir))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("config: create workspace root: %w", err)
	}
	return root, nil
}

// WorkspaceLayout is the complete, explicitly injected path model for
// one workspace. Low-level stores receive these roots from the Host;
// they never re-derive them from the process working directory or from
// a project-local .opencraft directory.
type WorkspaceLayout struct {
	DataDir string
	WorkDir string

	ID   string
	Root string // <dataDir>/workspaces/<id>

	// Workspace-owned state files. Configuration is user-level only;
	// workspace opencraft.yaml files are no longer loaded as an
	// override layer.
	ApprovalsFile string // approvals.yaml

	// Conversation state. SessionsDir is the sessions.Store root: it
	// owns session.db plus one directory per conversation.
	SessionDBPath string // <SessionsDir>/session.db
	SessionsDir   string // sessions/<conversation-id>/media|files|rollout.jsonl

	// Workspace runtime state.
	UndoDir    string
	CacheDir   string // cache/tools
	AuditDir   string
	ExportsDir string
}

// ResolveWorkspace builds the path layout for workDir under dataDir.
// It creates the workspace root directory but no subdirectories.
func ResolveWorkspace(dataDir, workDir string) (WorkspaceLayout, error) {
	if strings.TrimSpace(dataDir) == "" {
		return WorkspaceLayout{}, fmt.Errorf("config: data dir is required")
	}
	if strings.TrimSpace(workDir) == "" {
		return WorkspaceLayout{}, fmt.Errorf("config: workspace path is required")
	}
	root, err := WorkspaceRoot(dataDir, workDir)
	if err != nil {
		return WorkspaceLayout{}, err
	}
	id := WorkspaceID(workDir)
	return WorkspaceLayout{
		DataDir:       dataDir,
		WorkDir:       filepath.Clean(workDir),
		ID:            id,
		Root:          root,
		ApprovalsFile: filepath.Join(root, "approvals.yaml"),
		SessionsDir:   filepath.Join(root, "sessions"),
		SessionDBPath: filepath.Join(root, "sessions", "session.db"),
		UndoDir:       filepath.Join(root, "undo"),
		CacheDir:      filepath.Join(root, "cache", "tools"),
		AuditDir:      filepath.Join(root, "audit"),
		ExportsDir:    filepath.Join(root, "exports"),
	}, nil
}

// Ensure creates every directory the workspace layout owns. It never
// touches the workspace project directory.
func (l WorkspaceLayout) Ensure() error {
	for _, dir := range []string{
		l.Root,
		l.SessionsDir,
		l.UndoDir,
		l.CacheDir,
		l.AuditDir,
		l.ExportsDir,
	} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("config: create %s: %w", dir, err)
		}
	}
	return nil
}

// MetaFile returns the JSON file recording workspace open metadata.
func (l WorkspaceLayout) MetaFile() string {
	return filepath.Join(l.Root, "meta.json")
}
