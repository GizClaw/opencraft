//go:build soak

package execd

import (
	"context"
	"runtime"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/local"
)

// TestSoakSessionsBounded starts and stops many local sandbox sessions
// and asserts the goroutine count stays bounded (no session leaks).
func TestSoakSessionsBounded(t *testing.T) {
	ctx := context.Background()
	runner := local.New(t.TempDir())
	defer func() { _ = runner.Close() }()

	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		sess, err := runner.Start(ctx, sandbox.SessionSpec{
			ID:   "sess",
			Argv: []string{"/bin/sh", "-c", "true"},
		})
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if _, err := sess.Wait(ctx); err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
		_ = sess.Close()
	}
	// Allow a small slack for runtime background goroutines.
	if after := runtime.NumGoroutine(); after > before+10 {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}
