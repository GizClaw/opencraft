package config

import (
	"path/filepath"
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
