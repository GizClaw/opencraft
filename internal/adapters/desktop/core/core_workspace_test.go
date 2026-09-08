package core

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestWorkspaceHistory(t *testing.T) {
	dataDir := t.TempDir()
	workA := t.TempDir()
	workB := t.TempDir()
	c := NewCore(dataDir, dataDir, "")

	if err := config.SaveWorkspace(dataDir, workA); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveWorkspace(dataDir, workB); err != nil {
		t.Fatal(err)
	}
	metas, err := c.Workspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(metas))
	}

	c.SetWorkDir(workA)
	next, active, err := c.RemoveWorkspace(config.WorkspaceID(workA))
	if err != nil {
		t.Fatal(err)
	}
	if !active || next != workB {
		t.Fatalf("remove active = (next=%q active=%v), want %q", next, active, workB)
	}
}

func TestRemoveLastActiveWorkspaceReturnsToPicker(t *testing.T) {
	dataDir := t.TempDir()
	workDir := t.TempDir()
	c := NewCore(dataDir, dataDir, "")
	if err := config.SaveWorkspace(dataDir, workDir); err != nil {
		t.Fatal(err)
	}
	c.SetWorkDir(workDir)
	next, active, err := c.RemoveWorkspace(config.WorkspaceID(workDir))
	if err != nil {
		t.Fatal(err)
	}
	if !active || next != "" {
		t.Fatalf("remove last active = (next=%q active=%v), want active with no next", next, active)
	}
}
