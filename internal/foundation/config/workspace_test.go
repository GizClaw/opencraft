package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceIDStableAndPathSafe(t *testing.T) {
	a := filepath.Join(t.TempDir(), "proj a")
	b := filepath.Join(t.TempDir(), "proj b")
	idA1 := WorkspaceID(a)
	idA2 := WorkspaceID(a)
	idB := WorkspaceID(b)
	if idA1 != idA2 {
		t.Fatalf("workspace id unstable: %q vs %q", idA1, idA2)
	}
	if idA1 == idB {
		t.Fatal("distinct paths produced the same workspace id")
	}
	if !strings.HasPrefix(idA1, "s-") && len(idA1) != 32 {
		// Hash prefix ids are 32 hex chars; anything else would need a
		// review of the path-safety contract.
		t.Fatalf("unexpected id shape %q", idA1)
	}
	if strings.ContainsAny(idA1, `/\`) {
		t.Fatalf("workspace id is not path-safe: %q", idA1)
	}
}

func TestWorkspaceIDUsesAbsolutePath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(cwd, "proj")
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		t.Fatal(err)
	}
	if WorkspaceID(abs) != WorkspaceID(rel) {
		t.Fatalf("relative and absolute paths must share an id: %q vs %q",
			WorkspaceID(abs), WorkspaceID(rel))
	}
}

// TestCanonicalWorkDirFoldsSpellings pins the one spelling a workspace
// is written in. The window compares the path the backend announces
// against the history row the user clicked, character for character, so
// every spelling of one directory has to fold to one string — a trailing
// separator, a "." or ".." segment, stray whitespace, or a relative
// path, which a picker fallback hands in as readily as an absolute one.
func TestCanonicalWorkDirFoldsSpellings(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		t.Fatalf("relative spelling of %q from %q: %v", dir, cwd, err)
	}
	for _, spelling := range []string{
		dir + "/",
		dir + "//",
		filepath.Join(dir, "."),
		filepath.Join(dir, "sub", ".."),
		"  " + dir + "  ",
		rel,
	} {
		if got := CanonicalWorkDir(spelling); got != dir {
			t.Fatalf("CanonicalWorkDir(%q) = %q, want %q", spelling, got, dir)
		}
	}
	// A blank path stays blank: cleaning it must never name the
	// process's own directory, which is what a window without a
	// workspace has to read as.
	if got := CanonicalWorkDir("   "); got != "" {
		t.Fatalf("CanonicalWorkDir of a blank path = %q, want it empty", got)
	}
}

// TestResolveWorkspaceRecordsTheCanonicalSpelling keeps the layout's
// work dir — the value the Host is assembled with — in the same spelling
// the ready event announces, so a relative path handed to one half
// cannot name a second workspace in the other.
func TestResolveWorkspaceRecordsTheCanonicalSpelling(t *testing.T) {
	dataDir := t.TempDir()
	dir := t.TempDir()
	layout, err := ResolveWorkspace(dataDir, dir+"/")
	if err != nil {
		t.Fatal(err)
	}
	if layout.WorkDir != dir {
		t.Fatalf("layout work dir = %q, want %q", layout.WorkDir, dir)
	}
	if layout.WorkDir != CanonicalWorkDir(layout.WorkDir) {
		t.Fatalf("layout work dir %q is not the canonical spelling",
			layout.WorkDir)
	}
}

func TestWorkspaceRootIsolatedByID(t *testing.T) {
	dataDir := t.TempDir()
	workA := filepath.Join(t.TempDir(), "a")
	workB := filepath.Join(t.TempDir(), "b")
	rootA, err := WorkspaceRoot(dataDir, workA)
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := WorkspaceRoot(dataDir, workB)
	if err != nil {
		t.Fatal(err)
	}
	if rootA == rootB {
		t.Fatalf("workspace roots must differ: %q", rootA)
	}
	if filepath.Dir(rootA) != filepath.Dir(rootB) {
		t.Fatalf("workspace roots must share the workspaces parent: %q / %q", rootA, rootB)
	}
}

func TestResolveWorkspaceLayout(t *testing.T) {
	dataDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := ResolveWorkspace(dataDir, workDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "workspaces", WorkspaceID(workDir))
	if layout.Root != want {
		t.Fatalf("root = %q, want %q", layout.Root, want)
	}
	if layout.SessionDBPath != filepath.Join(want, "sessions", "session.db") {
		t.Fatalf("session db = %q", layout.SessionDBPath)
	}
	if layout.CacheDir != filepath.Join(want, "cache", "tools") {
		t.Fatalf("cache dir = %q", layout.CacheDir)
	}
	// ResolveWorkspace creates only the root; Ensure creates the rest.
	if _, err := os.Stat(layout.CacheDir); !os.IsNotExist(err) {
		t.Fatalf("Ensure must not run during ResolveWorkspace: %v", err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		layout.SessionsDir,
		layout.CacheDir,
		layout.AuditDir,
		layout.ExportsDir,
	} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("Ensure missed %s: %v", dir, err)
		}
	}
	// No project-local directory is ever created.
	if _, err := os.Stat(filepath.Join(workDir, ".opencraft")); !os.IsNotExist(err) {
		t.Fatalf("project .opencraft must not be created: %v", err)
	}
}

func TestResolveWorkspaceRejectsMissingInputs(t *testing.T) {
	if _, err := ResolveWorkspace("", t.TempDir()); err == nil {
		t.Fatal("empty data dir accepted")
	}
	if _, err := ResolveWorkspace(t.TempDir(), ""); err == nil {
		t.Fatal("empty workspace path accepted")
	}
}
