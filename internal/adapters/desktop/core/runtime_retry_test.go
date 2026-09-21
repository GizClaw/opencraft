package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

func TestEnsureUsableHostWithinWindow(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("sentinel last error")
	r := NewRuntime(t.TempDir(), t.TempDir())

	// A fast ensure inside the window returns nil.
	var calls int
	r.ensureHost = func(_ context.Context, _ string) (*host.Host, error) {
		calls++
		return nil, nil
	}
	if err := r.EnsureUsableHostWithin(
		ctx,
		time.Now().Add(time.Second),
		"/workspace/a",
		sentinel,
	); err != nil {
		t.Fatalf("EnsureUsableHostWithin = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("ensure calls = %d, want 1", calls)
	}

	// A blocking ensure is cut off at the deadline, and the caller's
	// last error surfaces unchanged.
	blockingErr := errors.New("ensure interrupted")
	start := time.Now()
	r.ensureHost = func(attemptCtx context.Context, _ string) (*host.Host, error) {
		<-attemptCtx.Done()
		return nil, blockingErr
	}
	err := r.EnsureUsableHostWithin(
		ctx,
		time.Now().Add(100*time.Millisecond),
		"/workspace/a",
		sentinel,
	)
	if err != sentinel {
		t.Fatalf("EnsureUsableHostWithin = %v, want the sentinel last error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("blocking ensure was not cut off: took %v", elapsed)
	}

	// An already-expired window never calls ensure.
	calls = 0
	if err := r.EnsureUsableHostWithin(
		ctx,
		time.Now().Add(-time.Second),
		"/workspace/a",
		sentinel,
	); err != sentinel {
		t.Fatalf("expired window returned %v, want the sentinel last error", err)
	}
	if calls != 0 {
		t.Fatalf("expired window still called ensure %d times", calls)
	}
}

// TestEnsureHostInWorkspaceWithinWindow mirrors the active-workspace
// case for background workspaces: the wait is bounded by the retry
// window and never resolves through the current Host.
func TestEnsureHostInWorkspaceWithinWindow(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("sentinel last error")
	r := NewRuntime(t.TempDir(), t.TempDir())

	var asked []string
	r.ensureBackgroundHost = func(
		_ context.Context, workDir string,
	) (*host.Host, error) {
		asked = append(asked, workDir)
		return nil, nil
	}
	if err := r.EnsureHostInWorkspaceWithin(
		ctx, time.Now().Add(time.Second), "/workspace/other", sentinel,
	); err != nil {
		t.Fatalf("EnsureHostInWorkspaceWithin = %v, want nil", err)
	}
	if len(asked) != 1 || asked[0] != "/workspace/other" {
		t.Fatalf("ensure asked for %v, want the background workspace", asked)
	}
	if got := r.Current(); got != nil {
		t.Fatalf("current host = %v, want untouched", got)
	}

	// A blocking ensure is cut off at the deadline and the caller's
	// last error surfaces unchanged.
	blockingErr := errors.New("ensure interrupted")
	r.ensureBackgroundHost = func(
		attemptCtx context.Context, _ string,
	) (*host.Host, error) {
		<-attemptCtx.Done()
		return nil, blockingErr
	}
	start := time.Now()
	err := r.EnsureHostInWorkspaceWithin(
		ctx, time.Now().Add(100*time.Millisecond), "/workspace/other", sentinel,
	)
	if err != sentinel {
		t.Fatalf("blocking ensure returned %v, want the sentinel", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("blocking ensure was not cut off: took %v", elapsed)
	}

	// An already-expired window never calls ensure.
	calls := 0
	r.ensureBackgroundHost = func(
		_ context.Context, _ string,
	) (*host.Host, error) {
		calls++
		return nil, nil
	}
	if err := r.EnsureHostInWorkspaceWithin(
		ctx, time.Now().Add(-time.Second), "/workspace/other", sentinel,
	); err != sentinel {
		t.Fatalf("expired window returned %v, want the sentinel", err)
	}
	if calls != 0 {
		t.Fatalf("expired window still called ensure %d times", calls)
	}
}
