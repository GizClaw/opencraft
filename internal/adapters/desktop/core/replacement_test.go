package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// The application half of the pool's replacement policy. The workspace
// half (core_workspace_test.go) is about which workspace the window is
// on; this one is about whether an application still wants a runtime at
// all, which is a question the registry answers and the answer changes
// while a generation drains.

// installAppContent writes the layout an installed application has —
// just the manifest the registry reads — without copying a package.
// These tests are about the pool's two questions, not about installing:
// capabilities/apps owns the install path, and going through it here
// would make a dispatch test depend on the preflight.
func installAppContent(t *testing.T, dataDir, id string) {
	t.Helper()
	content := filepath.Join(dataDir, "apps", id, "content")
	if err := os.MkdirAll(content, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := "app: v1\nid: " + id + "\nname: Test\nversion: 0.1.0\nlayers:\n  - layer.yaml\n"
	if err := os.WriteFile(filepath.Join(content, "app.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReplacementWantedSpeaksForTheApplicationRegistry pins the second
// answer: an application is rebuilt after a drain only while the
// registry still serves it. A disable or an uninstall that lands while
// the old generation drains is a decision not to come back, and a
// successor assembled anyway would be a runtime nobody asked for —
// with a document the registry now refuses.
func TestReplacementWantedSpeaksForTheApplicationRegistry(t *testing.T) {
	dataDir := t.TempDir()
	c := NewCore(dataDir, dataDir, "")
	t.Cleanup(c.Runtime.Close)
	store := c.Runtime.Apps()
	if store == nil {
		t.Fatal("this launch built no application registry")
	}
	installAppContent(t, dataDir, "hello")

	// An install arrives enabled, so its runtime is wanted.
	if !c.replacementWanted(host.AppTarget("hello")) {
		t.Fatal("an installed, enabled application is not wanted")
	}
	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if c.replacementWanted(host.AppTarget("hello")) {
		t.Fatal("a disabled application is wanted: a drain would rebuild it")
	}
	if err := store.SetEnabled("hello", true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if !c.replacementWanted(host.AppTarget("hello")) {
		t.Fatal("a re-enabled application is not wanted")
	}
	if err := store.Uninstall("hello", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if c.replacementWanted(host.AppTarget("hello")) {
		t.Fatal("an uninstalled application is wanted")
	}
	if c.replacementWanted(host.AppTarget("never-installed")) {
		t.Fatal("an application that was never installed is wanted")
	}

	// The two scopes do not answer for each other's targets: the
	// window's own workspace is still wanted, and an application target
	// is still not a workspace one.
	workDir := t.TempDir()
	c.SetWorkDir(workDir)
	if !c.replacementWanted(host.WorkspaceTarget(workDir)) {
		t.Fatal("the window's workspace is not wanted")
	}
	if c.replacementWanted(host.AppTarget(workDir)) {
		t.Fatal("an application id holding a working directory is wanted")
	}
}

// TestReplacementWantedRefusesApplicationsWithoutARegistry pins the
// launch that has none: without an app home there is nothing to resolve
// a target against, so no application is wanted and no successor is
// assembled for one. "Cannot build it" must not become "assemble
// something else".
func TestReplacementWantedRefusesApplicationsWithoutARegistry(t *testing.T) {
	c := &Core{Runtime: NewRuntime(t.TempDir(), t.TempDir(), "")}
	t.Cleanup(c.Runtime.Close)
	if c.Runtime.Apps() != nil {
		t.Fatal("a launch without an app home built a registry")
	}
	if c.replacementWanted(host.AppTarget("hello")) {
		t.Fatal("an application is wanted on a launch that cannot resolve one")
	}
}

// TestReplacementInstalledAnnouncesPerScope pins what the pool's second
// callback does for each scope. A caller that announces an application
// as "ready" would make the UI refresh the workspace's own view — the
// wrong deployment's sessions, models and workspace — so the app half
// speaks its own event.
func TestReplacementInstalledAnnouncesPerScope(t *testing.T) {
	dataDir := t.TempDir()
	c := NewCore(dataDir, dataDir, "")
	t.Cleanup(c.Runtime.Close)
	workDir := t.TempDir()
	c.SetWorkDir(workDir)

	var events []uiEvent
	c.Shell.SetNotificationSink(func(typ string, data any) {
		events = append(events, uiEvent{typ: typ, data: data})
	})

	c.replacementInstalled(host.AppTarget("hello"))
	if len(events) != 1 || events[0].typ != EventAppStatus {
		t.Fatalf("events after an application landed = %+v", events)
	}
	if got, ok := events[0].data.(AppStatusEvent); !ok || got.ID != "hello" || !got.Serving {
		t.Fatalf("application status event = %#v", events[0].data)
	}

	events = nil
	c.replacementInstalled(host.WorkspaceTarget(workDir))
	if len(events) != 1 || events[0].typ != EventReady {
		t.Fatalf("events after a workspace landed = %+v", events)
	}
	// The workspace's ready event names that workspace: it is what the
	// window switches to.
	if got, ok := events[0].data.(ConfigStatus); !ok || got.WorkDir != workDir {
		t.Fatalf("ready event = %#v", events[0].data)
	}
}

// uiEvent is one observed UI event: the name the frontend dispatches on
// and the payload it receives.
type uiEvent struct {
	typ  string
	data any
}
