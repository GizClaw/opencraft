package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceHistoryRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	first := filepath.Join(t.TempDir(), "repo-a")
	second := filepath.Join(t.TempDir(), "repo-b")

	if err := saveWorkspaceMeta(root, first); err != nil {
		t.Fatal(err)
	}
	// Re-opening the same workspace refreshes (single record), and
	// opening another one adds a second entry.
	time.Sleep(2 * time.Millisecond)
	if err := saveWorkspaceMeta(root, first); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceMeta(root, second); err != nil {
		t.Fatal(err)
	}

	list, err := loadWorkspaces(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(list))
	}
	// Most recently opened first.
	if list[0].Path != second || list[1].Path != first {
		t.Fatalf("order = %+v", list)
	}
	if list[0].Title != "repo-b" || list[0].ID == "" {
		t.Fatalf("meta = %+v", list[0])
	}

	// Removing one entry keeps the other.
	if err := removeWorkspaceMeta(root, list[0].ID); err != nil {
		t.Fatal(err)
	}
	list, err = loadWorkspaces(root)
	if err != nil || len(list) != 1 || list[0].Path != first {
		t.Fatalf("after remove = %+v, %v", list, err)
	}
	// Idempotent removal of an unknown id.
	if err := removeWorkspaceMeta(root, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := removeWorkspaceMeta(root, "00000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceIDValidation(t *testing.T) {
	if isWorkspaceID("short") {
		t.Fatal("short id must be rejected")
	}
	if isWorkspaceID("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz") {
		t.Fatal("non-hex id must be rejected")
	}
	if !isWorkspaceID("0123456789abcdef0123456789abcdef") {
		t.Fatal("hex id must be accepted")
	}
}

func TestProjectTrustRoundTrip(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := writeProjectTrust(repo, true); err != nil {
		t.Fatal(err)
	}
	app, err := New(Options{UserDir: filepath.Join(t.TempDir(), "config")})
	if err != nil {
		t.Fatal(err)
	}
	if !app.isProjectTrusted(repo) {
		t.Fatal("trusted workspace must report trusted")
	}
	if err := writeProjectTrust(repo, false); err != nil {
		t.Fatal(err)
	}
	if app.isProjectTrusted(repo) {
		t.Fatal("untrusted workspace must report untrusted")
	}
	// Untouched workspaces default to untrusted.
	other := filepath.Join(t.TempDir(), "other")
	if app.isProjectTrusted(other) {
		t.Fatal("unknown workspace must default to untrusted")
	}
}

func TestProjectConfigStatus(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".opencraft", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, ".opencraft", "config", "opencraft.yaml"),
		[]byte("version: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(Options{
		WorkDir: workDir,
		UserDir: filepath.Join(t.TempDir(), "config"),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := app.ProjectConfigStatus()
	if !st.Present || st.Trusted || st.Path == "" {
		t.Fatalf("status = %+v, want present+untrusted", st)
	}
	if err := writeProjectTrust(workDir, true); err != nil {
		t.Fatal(err)
	}
	st = app.ProjectConfigStatus()
	if !st.Trusted {
		t.Fatalf("status after trust = %+v, want trusted", st)
	}
}
