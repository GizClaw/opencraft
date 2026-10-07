package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// Application state roots: <dataDir>/apps/<id>. An application is an
// installed content tree (manifest + flowcraft deployment layers + the
// resources they reference) whose runtime gets its own state root,
// session database and private workspace — never the user's project
// directory, and never the application's content root.

// appIDRe constrains application ids. It is the plugin id rule: a
// lowercase start, then lowercase letters, digits, dots, underscores
// and hyphens. The id names a directory under the apps root, so a
// leading dot ("..", ".ssh") and a separator are not spellable at all.
var appIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// appsDirName is the one spelling of the directory under a data dir (or
// an app home) that holds installed applications: <root>/apps/<id>.
const appsDirName = "apps"

// ValidAppID reports whether id is a well-formed application id: the
// name a manifest declares, the name of the install directory under the
// content root, and the last segment of the state root all at once.
func ValidAppID(id string) bool {
	return appIDRe.MatchString(id)
}

// AppsRoot returns <dataDir>/apps — the directory holding one state
// root per installed application — creating it when needed.
func AppsRoot(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("config: data dir is required")
	}
	dir := filepath.Join(dataDir, appsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: create apps root: %w", err)
	}
	return dir, nil
}

// AppLayout builds the path layout for one installed application under
// dataDir. It creates the application root directory but no
// subdirectories; Ensure creates the rest, the private workspace
// included.
//
// The shape mirrors ResolveWorkspace with two differences that are the
// point of the split: WorkDir is the application's own workspace
// (<root>/workspace, created by Ensure like any other state directory),
// and ID is the usage/diagnostic key "app:<id>" rather than a path
// hash. The content root the layers were installed from is not part of
// this layout: nothing here may write to it.
func AppLayout(dataDir, id string) (WorkspaceLayout, error) {
	root, err := AppStateRoot(dataDir, id)
	if err != nil {
		return WorkspaceLayout{}, err
	}
	if _, err := AppsRoot(dataDir); err != nil {
		return WorkspaceLayout{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return WorkspaceLayout{}, fmt.Errorf("config: create application root: %w", err)
	}
	return WorkspaceLayout{
		DataDir:       dataDir,
		WorkDir:       filepath.Join(root, "workspace"),
		ID:            "app:" + id,
		Root:          root,
		ApprovalsFile: filepath.Join(root, "approvals.yaml"),
		SessionsDir:   filepath.Join(root, "sessions"),
		SessionDBPath: filepath.Join(root, "sessions", "session.db"),
		CacheDir:      filepath.Join(root, "cache", "tools"),
		AuditDir:      filepath.Join(root, "audit"),
		ExportsDir:    filepath.Join(root, "exports"),
	}, nil
}

// AppStateRoot returns <dataDir>/apps/<id> without creating anything.
// AppLayout is the creating caller; a reader that only needs to name
// (or remove) an installed application's state — the registry's
// uninstall path — asks for the path and touches nothing.
func AppStateRoot(dataDir, id string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("config: data dir is required")
	}
	if !ValidAppID(id) {
		return "", fmt.Errorf(
			"config: application id %q: want %s", id, appIDRe)
	}
	return filepath.Join(dataDir, appsDirName, id), nil
}

// ownsWorkDir reports whether WorkDir is a directory this layout owns
// rather than one the user owns. It is true exactly when WorkDir is a
// proper descendant of Root — the application private workspace — and
// false for a workspace layout, whose WorkDir is the user's project.
func (l WorkspaceLayout) ownsWorkDir() bool {
	if l.WorkDir == "" || l.Root == "" {
		return false
	}
	work := filepath.Clean(l.WorkDir)
	root := filepath.Clean(l.Root)
	return work != root && pathsafe.Within(root, work)
}
