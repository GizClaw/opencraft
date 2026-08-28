package desktop

import (
	"path/filepath"
	"testing"
	"time"
)

// TestOpenWorkspaceDoesNotSelfDeadlock is a regression test for the
// workspace-switch hang: OpenWorkspace used to call closeRollouts
// while holding a.mu, and closeRollouts locks a.mu again. sync.Mutex
// is not reentrant, so any switch (the "+" picker or clicking a saved
// workspace) deadlocked the app — and with it every binding that takes
// a.mu, which is why the frontend went unresponsive.
func TestOpenWorkspaceDoesNotSelfDeadlock(t *testing.T) {
	work := t.TempDir()
	a := fileManagerApp(t, t.TempDir())
	a.workDir = work
	// A missing user config dir makes rebuild fail fast (before the
	// runtime/execd machinery starts), so the test only exercises the
	// lock handoff: any return means no deadlock.
	a.userDir = filepath.Join(t.TempDir(), "missing")

	done := make(chan error, 1)
	go func() { done <- a.OpenWorkspace(work) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a rebuild error from a missing user config dir")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OpenWorkspace deadlocked: closeRollouts re-locked a.mu")
	}
}
