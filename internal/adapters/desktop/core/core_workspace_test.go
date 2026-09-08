package core

import (
	"os"
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

func TestSetWorkDirPublishesPluginEnv(t *testing.T) {
	for _, key := range []string{
		"OPEN_CRAFT_WORKDIR",
		"OPEN_CRAFT_CACHE",
		"OPEN_CRAFT_DATA_DIR",
		"OPEN_CRAFT_WORKSPACE_DIR",
		"OPEN_CRAFT_SESSIONS_DIR",
		"OPEN_CRAFT_APPROVALS",
		"OPEN_CRAFT_TOOL_CACHE",
		"OPEN_CRAFT_AUDIT_DIR",
	} {
		t.Setenv(key, "")
	}
	dataDir := t.TempDir()
	workDir := t.TempDir()
	c := NewCore(dataDir, dataDir, "")

	c.SetWorkDir(workDir)
	if got := os.Getenv("OPEN_CRAFT_WORKDIR"); got != workDir {
		t.Fatalf("OPEN_CRAFT_WORKDIR = %q, want %q", got, workDir)
	}
	if got := os.Getenv("OPEN_CRAFT_DATA_DIR"); got != dataDir {
		t.Fatalf("OPEN_CRAFT_DATA_DIR = %q, want %q", got, dataDir)
	}

	c.SetWorkDir("")
	if got := os.Getenv("OPEN_CRAFT_WORKDIR"); got != "" {
		t.Fatalf("OPEN_CRAFT_WORKDIR = %q after clearing, want empty", got)
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
