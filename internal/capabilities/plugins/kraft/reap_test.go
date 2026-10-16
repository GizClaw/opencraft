package kraft

import (
	"context"
	"testing"
	"time"
)

// TestCrashedPluginIsReaped pins the exit path: a kraft process that
// dies must go through cmd.Wait, or it stays a zombie and its pipes
// leak for the life of the host process.
func TestCrashedPluginIsReaped(t *testing.T) {
	m, _ := newTestManagerWithHelper(t, "4") // crashingPlugin
	p, err := m.get(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}

	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin process did not exit")
	}
	// The exit watcher runs cmd.Wait; waiting on it (Shutdown does the
	// same) makes the reap observable instead of racy.
	m.exitWatchers.Wait()
	if p.cmd.ProcessState == nil {
		t.Fatal("kraft process was never reaped: cmd.Wait was not called")
	}
	if !p.cmd.ProcessState.Exited() {
		t.Fatalf("process state = %v, want an exited process", p.cmd.ProcessState)
	}
}
