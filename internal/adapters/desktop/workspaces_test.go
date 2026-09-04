package desktop

import (
	"os"
	"path/filepath"
	"sync"
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

func TestLastWorkspaceRestoresMostRecentExisting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	older := filepath.Join(t.TempDir(), "repo-a")
	newer := filepath.Join(t.TempDir(), "repo-b")
	// repo-gone is recorded but never created: a stale history entry
	// whose directory no longer exists must be skipped.
	gone := filepath.Join(t.TempDir(), "repo-gone")
	for _, dir := range []string{older, newer} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := saveWorkspaceMeta(root, older); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := saveWorkspaceMeta(root, newer); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := saveWorkspaceMeta(root, gone); err != nil {
		t.Fatal(err)
	}

	got := lastWorkspaceFromDir(root)
	if got != newer {
		t.Fatalf("lastWorkspaceFromDir = %q, want %q", got, newer)
	}
}

func TestLastWorkspaceSkipsHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceMeta(root, home); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceMeta(root, repo); err != nil {
		t.Fatal(err)
	}

	got := lastWorkspaceFromDir(root)
	if got != repo {
		t.Fatalf("lastWorkspaceFromDir = %q, want %q (home must not be auto-restored)", got, repo)
	}
}

func TestLastWorkspaceEmptyHistory(t *testing.T) {
	if got := lastWorkspaceFromDir(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("lastWorkspaceFromDir = %q, want empty", got)
	}
}

func TestStartupWorkDir(t *testing.T) {
	// An explicitly passed workspace always wins, even when history
	// points somewhere else.
	history := filepath.Join(t.TempDir(), "workspaces")
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceMeta(history, repo); err != nil {
		t.Fatal(err)
	}
	if got := startupWorkDir("/explicit/project", history); got != "/explicit/project" {
		t.Fatalf("startupWorkDir(explicit) = %q, want the explicit path", got)
	}
	// History restores the most recently opened workspace.
	if got := startupWorkDir("", history); got != repo {
		t.Fatalf("startupWorkDir(history) = %q, want %q", got, repo)
	}
	// A fresh install (empty history) starts with no workspace: the
	// process cwd and the user's home must never be adopted, so the
	// welcome screen shows on every platform (Finder "/", Explorer
	// exe folder).
	if got := startupWorkDir("", ""); got != "" {
		t.Fatalf("startupWorkDir(fresh) = %q, want empty", got)
	}
}

func TestRebuildWithNoWorkspaceIsANoop(t *testing.T) {
	app := &App{workDir: ""}
	if err := app.rebuild(); err != nil {
		t.Fatalf("rebuild with no workspace: %v", err)
	}
}

func TestRemoveCurrentWorkspaceSwitchesToNext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	history := filepath.Join(home, ".opencraft", "workspaces")
	current := t.TempDir()
	next := t.TempDir()
	if err := saveWorkspaceMeta(history, current); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceMeta(history, next); err != nil {
		t.Fatal(err)
	}

	a := &App{workDir: current}
	if err := a.RemoveWorkspace(workspaceID(current)); err != nil {
		t.Fatalf("RemoveWorkspace(current): %v", err)
	}
	if got := a.snapshotWorkDir(); got != next {
		t.Fatalf("work dir after removing current = %q, want %q", got, next)
	}
	metas, err := loadWorkspaces(history)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Path != next {
		t.Fatalf("history after removal = %+v, want only %q", metas, next)
	}
	if a.userDB != nil {
		_ = a.userDB.Close()
	}
}

func TestRemoveCurrentWorkspaceClosesWhenNoOther(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	history := filepath.Join(home, ".opencraft", "workspaces")
	current := t.TempDir()
	if err := saveWorkspaceMeta(history, current); err != nil {
		t.Fatal(err)
	}

	a := &App{workDir: current}
	if err := a.RemoveWorkspace(workspaceID(current)); err != nil {
		t.Fatalf("RemoveWorkspace(current): %v", err)
	}
	if got := a.snapshotWorkDir(); got != "" {
		t.Fatalf("work dir after removing sole current = %q, want empty", got)
	}
	metas, err := loadWorkspaces(history)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("history after removal = %+v, want empty", metas)
	}
}

func TestRemoveNonCurrentWorkspaceKeepsCurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	history := filepath.Join(home, ".opencraft", "workspaces")
	current := t.TempDir()
	other := t.TempDir()
	if err := saveWorkspaceMeta(history, current); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceMeta(history, other); err != nil {
		t.Fatal(err)
	}

	a := &App{workDir: current}
	if err := a.RemoveWorkspace(workspaceID(other)); err != nil {
		t.Fatalf("RemoveWorkspace(other): %v", err)
	}
	if got := a.snapshotWorkDir(); got != current {
		t.Fatalf("work dir after removing other = %q, want current %q", got, current)
	}
	metas, err := loadWorkspaces(history)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Path != current {
		t.Fatalf("history after removal = %+v, want only %q", metas, current)
	}
}

func TestCloseWorkspaceDefersWhileTurnRuns(t *testing.T) {
	a := &App{
		mu:      sync.Mutex{},
		workDir: t.TempDir(),
		runConvs: map[string]string{
			"run-1": "s-1",
		},
	}
	if err := a.closeWorkspace(); err != nil {
		t.Fatalf("closeWorkspace during turn: %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pendingNoWorkspace || !a.pendingRebuild {
		t.Fatal("close workspace must be deferred while a turn is running")
	}
	if a.workDir == "" {
		t.Fatal("work dir must not clear until the deferred close applies")
	}
}

func TestMaybeApplyPendingCloseWorkspaceWhenIdle(t *testing.T) {
	a := &App{
		mu:                 sync.Mutex{},
		workDir:            t.TempDir(),
		runConvs:           map[string]string{},
		pendingNoWorkspace: true,
		pendingRebuild:     true,
	}
	a.maybeApplyPendingRebuild()
	if got := a.snapshotWorkDir(); got != "" {
		t.Fatalf("work dir after pending close = %q, want empty", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingNoWorkspace || a.pendingRebuild {
		t.Fatal("pending close must be consumed")
	}
}

func TestProjectConfigStatus(t *testing.T) {
	app, err := New(Options{
		UserDir: filepath.Join(t.TempDir(), "config"),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := app.ProjectConfigStatus()
	if st.Present || st.Trusted || st.Path != "" {
		t.Fatalf("status = %+v, want empty compatibility status", st)
	}
	if err := app.SetProjectTrust(t.TempDir(), true); err != nil {
		t.Fatalf("SetProjectTrust must be a no-op: %v", err)
	}
}
