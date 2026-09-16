package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// newAbsWorkspace builds a HostWorkspace over root with the given
// session store.
func newAbsWorkspace(t *testing.T, root string, store *sessions.Store) *HostWorkspace {
	t.Helper()
	confined, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return &HostWorkspace{
		sessions: store,
		confined: confined,
		host:     &hostWorkspace{root: root},
		root:     root,
	}
}

// TestHostWorkspaceAbsPathConfinesToRoot pins the plugin-source
// resolution used by the agent's plugin install tool: workspace mode
// accepts workspace-relative paths and rejects absolute paths,
// traversal and symlink escapes.
func TestHostWorkspaceAbsPathConfinesToRoot(t *testing.T) {
	skipIfYoloOnly(t)
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	hw := newAbsWorkspace(t, root, store)
	ctx := sessionCtx("s1")

	got, err := hw.AbsPath(ctx, filepath.Join("plugins", "hello"))
	if err != nil {
		t.Fatalf("AbsPath(relative): %v", err)
	}
	if want := filepath.Join(realRoot, "plugins", "hello"); got != want {
		t.Fatalf("AbsPath(relative) = %q, want %q", got, want)
	}

	if got, err := hw.AbsPath(ctx, ""); err != nil || got != realRoot {
		t.Fatalf("AbsPath(empty) = (%q, %v), want the workspace root", got, err)
	}
	if _, err := hw.AbsPath(ctx, filepath.Join("..", "escape")); err == nil {
		t.Fatal("traversal must be rejected in workspace mode")
	}
	if _, err := hw.AbsPath(ctx, "/etc"); err == nil {
		t.Fatal("absolute paths must be rejected in workspace mode")
	}

	// A symlink inside the workspace that points outside must not
	// become an escape hatch for the installer source.
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := hw.AbsPath(ctx, "link/plugin"); err == nil {
		t.Fatalf("symlink escape resolved to %q, want rejection", got)
	}
}

// TestHostWorkspaceAbsPathYoloAllowsHostPaths pins the YOLO-mode
// counterpart: relative paths still resolve against the workspace
// root, absolute host paths pass through (the session has already
// opted out of confinement).
func TestHostWorkspaceAbsPathYoloAllowsHostPaths(t *testing.T) {
	skipIfYoloOnly(t)
	root := t.TempDir()
	store := newTestStore(t)
	hw := newAbsWorkspace(t, root, store)
	if err := store.SetMode(
		context.Background(), "s1", sessions.ModeYOLO,
	); err != nil {
		t.Fatal(err)
	}
	ctx := sessionCtx("s1")

	outside := filepath.Join(t.TempDir(), "plugin")
	got, err := hw.AbsPath(ctx, outside)
	if err != nil {
		t.Fatalf("AbsPath(absolute, yolo): %v", err)
	}
	if got != outside {
		t.Fatalf("AbsPath(absolute, yolo) = %q, want %q", got, outside)
	}
	got, err = hw.AbsPath(ctx, "plugins/hello")
	if err != nil {
		t.Fatalf("AbsPath(relative, yolo): %v", err)
	}
	if want := filepath.Join(root, "plugins", "hello"); got != want {
		t.Fatalf("AbsPath(relative, yolo) = %q, want %q", got, want)
	}
}
