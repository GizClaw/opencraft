package core

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// TestReplacementWantedSpeaksForTheWindowsWorkspace pins the adapter's
// half of the deferred-replacement policy. It decides whether a retired
// assembly is rebuilt at all, so both answers are load-bearing: the
// window's own workspace is rebuilt (whatever spelling it arrives in),
// and everything else — another workspace, an application whose id
// happens to look like a directory, a window with no workspace open — is
// left retired. An application target is the case a kind-blind
// comparison gets wrong when an app id is a path.
func TestReplacementWantedSpeaksForTheWindowsWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	c := NewCore(dataDir, dataDir, t.TempDir())
	workDir := c.ActiveWorkDir()

	cases := []struct {
		name   string
		target host.Target
		want   bool
	}{
		{"the window's workspace", host.WorkspaceTarget(workDir), true},
		{"the same path, uncleaned",
			host.WorkspaceTarget(workDir + "/./"), true},
		{"another workspace", host.WorkspaceTarget(workDir + "-elsewhere"), false},
		{"an application with the path as its id", host.AppTarget(workDir), false},
		{"an unnamed target", host.Target{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.replacementWanted(tc.target); got != tc.want {
				t.Fatalf("replacementWanted(%s) = %v, want %v",
					tc.target, got, tc.want)
			}
		})
	}

	// With no workspace open there is nothing to rebuild for, so no
	// target is wanted — the window's own path included.
	c.SetWorkDir("")
	for _, target := range []host.Target{
		host.WorkspaceTarget(workDir),
		{},
	} {
		if c.replacementWanted(target) {
			t.Fatalf("replacementWanted(%s) = true with no workspace open",
				target)
		}
	}
}

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
