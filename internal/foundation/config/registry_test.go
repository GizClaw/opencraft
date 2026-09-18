package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWorkspaceMetaRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	workA := filepath.Join(t.TempDir(), "a")
	workB := filepath.Join(t.TempDir(), "b")
	for _, dir := range []string{workA, workB} {
		if err := SaveWorkspace(dataDir, dir); err != nil {
			t.Fatal(err)
		}
	}
	metas, err := ListWorkspaces(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("metas = %d, want 2", len(metas))
	}
	// Newest first; SaveWorkspace stamps LastOpened in order.
	if metas[0].ID != WorkspaceID(workB) {
		t.Fatalf("first meta = %+v, want workB", metas[0])
	}
	for _, m := range metas {
		if m.ID != WorkspaceID(m.Path) {
			t.Fatalf("meta id mismatch: %+v", m)
		}
	}
}

func TestRemoveWorkspaceOnlyRemovesState(t *testing.T) {
	dataDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "repo")
	if err := SaveWorkspace(dataDir, workDir); err != nil {
		t.Fatal(err)
	}
	id := WorkspaceID(workDir)
	if err := RemoveWorkspace(dataDir, id); err != nil {
		t.Fatal(err)
	}
	metas, err := ListWorkspaces(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("metas after remove = %+v", metas)
	}
	if _, err := filepath.Abs(workDir); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(dataDir, "bad-id"); err == nil {
		t.Fatal("invalid workspace id accepted")
	}
	if err := RemoveWorkspace(dataDir, id); err != nil {
		t.Fatalf("second remove should be idempotent: %v", err)
	}
}

func TestIsWorkspaceID(t *testing.T) {
	valid := WorkspaceID(t.TempDir())
	if !IsWorkspaceID(valid) {
		t.Fatalf("%q should be a valid workspace id", valid)
	}
	for _, bad := range []string{"", "short", "../../etc", valid + "x"} {
		if IsWorkspaceID(bad) {
			t.Fatalf("invalid workspace id accepted: %q", bad)
		}
	}
}

// stampMeta rewrites one workspace entry with a hand-picked
// last_opened so ranking can be exercised without sleeping between
// opens.
func stampMeta(t *testing.T, dataDir, workDir, lastOpened string) {
	t.Helper()
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cleaned := filepath.Clean(workDir)
	data, err := json.Marshal(WorkspaceMeta{
		ID:         WorkspaceID(workDir),
		Path:       cleaned,
		Title:      filepath.Base(cleaned),
		LastOpened: lastOpened,
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := WorkspaceRoot(dataDir, workDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListWorkspacesRanksTiesDeterministically(t *testing.T) {
	dataDir := t.TempDir()
	base := t.TempDir()
	newest := filepath.Join(base, "newest")
	tieAA := filepath.Join(base, "aa")
	tieZZ := filepath.Join(base, "zz")
	broken := filepath.Join(base, "broken")

	sameTime := "2026-09-06T00:00:00Z"
	stampMeta(t, dataDir, tieZZ, sameTime)
	stampMeta(t, dataDir, tieAA, sameTime)
	stampMeta(t, dataDir, newest, "2026-10-01T00:00:00Z")
	stampMeta(t, dataDir, broken, "not-a-timestamp")

	metas, err := ListWorkspaces(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(metas))
	for _, m := range metas {
		got = append(got, m.Title)
	}
	// Newest first; the tied pair falls back to title order instead of
	// the directory read order, and an unparsable stamp sinks last.
	want := []string{"newest", "aa", "zz", "broken"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
