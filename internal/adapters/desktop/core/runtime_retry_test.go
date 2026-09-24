package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// TestDoAbsorbsRetryableLifecycleGuards pins the contract every binding
// used to spell out by hand: a retryable host lifecycle error is
// absorbed by waiting for the workspace's replacement Host and running
// the call again, and the caller never sees it.
func TestDoAbsorbsRetryableLifecycleGuards(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir(), "")
	replacement := &host.Host{}
	var ensured int
	r.ensureHost = func(_ context.Context, workDir string) (*host.Host, error) {
		ensured++
		if workDir != "/workspace/a" {
			t.Fatalf("ensure asked for %q, want the caller's workspace", workDir)
		}
		return replacement, nil
	}

	var calls int
	err := r.Do(context.Background(), "/workspace/a", nil,
		func(h *host.Host) error {
			calls++
			if h != replacement {
				t.Fatalf("fn got host %p, want %p", h, replacement)
			}
			if calls == 1 {
				return host.ErrRuntimeClosing
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Do = %v, want nil after absorbing the guard", err)
	}
	if calls != 2 {
		t.Fatalf("fn calls = %d, want 2 (one refused, one accepted)", calls)
	}
	if ensured != 2 {
		t.Fatalf("ensure calls = %d, want one resolution per attempt", ensured)
	}
}

// TestDoReportsNonRetryableErrorsAsTheyAre pins the other half: an
// error that is not a lifecycle guard is the caller's own failure, and
// repeating it would only delay the report. The same holds for a pool
// error that is not a guard — an assembly failure or an empty workspace
// must not be retried into the window.
func TestDoReportsNonRetryableErrorsAsTheyAre(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir(), "")
	r.ensureHost = func(_ context.Context, _ string) (*host.Host, error) {
		return &host.Host{}, nil
	}

	refused := errors.New("conversation is busy")
	var calls int
	err := r.Do(context.Background(), "/workspace/a", nil,
		func(*host.Host) error {
			calls++
			return refused
		})
	if !errors.Is(err, refused) {
		t.Fatalf("Do = %v, want %v", err, refused)
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1: a non-retryable error is reported", calls)
	}

	var ensured int
	r.ensureHost = func(_ context.Context, _ string) (*host.Host, error) {
		ensured++
		return nil, host.ErrNoWorkspace
	}
	err = r.Do(context.Background(), "", nil, func(*host.Host) error {
		t.Fatal("fn ran without a host")
		return nil
	})
	if !errors.Is(err, host.ErrNoWorkspace) {
		t.Fatalf("Do = %v, want the empty-workspace refusal", err)
	}
	if ensured != 1 {
		t.Fatalf("ensure calls = %d, want 1: a pool refusal is not retried", ensured)
	}
}

// TestDoStopsWhenTheCallerPremiseExpired pins the switch guard and
// where it is consulted. A caller whose window has already moved still
// gets its first attempt — the call is resolved against the workspace
// it named, not against whatever Host the window shows, so a send that
// raced a workspace switch lands where its conversation lives — and
// gives up on the retry instead of holding the RPC open for a
// workspace nobody is looking at. The stop happens to answer "moved"
// from the very first consultation: were the guard checked before the
// attempt, fn would never run.
func TestDoStopsWhenTheCallerPremiseExpired(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir(), "")
	r.ensureHost = func(_ context.Context, _ string) (*host.Host, error) {
		return &host.Host{}, nil
	}

	var calls int
	err := r.Do(context.Background(), "/workspace/a",
		func() bool { return true },
		func(*host.Host) error {
			calls++
			return host.ErrRuntimeClosing
		})
	if !errors.Is(err, host.ErrRuntimeClosing) {
		t.Fatalf("Do = %v, want the last lifecycle error", err)
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1: one attempt, then no retry",
			calls)
	}
}

// TestDoSurfacesTheCallersOwnCancellation pins the difference between
// the window running out and the request dying: only the former is
// absorbed. A canceled RPC reports the cancellation instead of a stale
// lifecycle guard nobody can act on.
func TestDoSurfacesTheCallersOwnCancellation(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir(), "")
	r.ensureHost = func(attemptCtx context.Context, _ string) (*host.Host, error) {
		<-attemptCtx.Done()
		return nil, attemptCtx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := r.Do(ctx, "/workspace/a", nil, func(*host.Host) error {
		t.Fatal("fn ran without a host")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do = %v, want the caller's own cancellation", err)
	}
}

// TestDoBoundsTheWaitForAReplacement pins the window: a workspace whose
// old Host never finishes draining does not hold the RPC open for the
// length of somebody else's run. The caller gets the guard it hit, not
// the wait's own deadline. The case shortens the window so it asserts
// on the bound without paying the production one.
func TestDoBoundsTheWaitForAReplacement(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir(), "")
	window := 200 * time.Millisecond
	r.window = window
	blocking := make(chan struct{})
	defer close(blocking)
	r.ensureHost = func(attemptCtx context.Context, _ string) (*host.Host, error) {
		<-attemptCtx.Done()
		return nil, attemptCtx.Err()
	}

	start := time.Now()
	err := r.Do(context.Background(), "/workspace/a", nil,
		func(*host.Host) error {
			t.Fatal("fn ran without a host")
			return nil
		})
	elapsed := time.Since(start)
	if !errors.Is(err, host.ErrRuntimeNotReady) {
		t.Fatalf("Do = %v, want the not-ready guard it never got past", err)
	}
	if elapsed < window {
		t.Fatalf("Do gave up after %v, want the window %v",
			elapsed, window)
	}
	if elapsed > window+2*time.Second {
		t.Fatalf("Do waited %v, want the wait bounded by %v",
			elapsed, window)
	}
}

// TestDoUsesAHostResolvedAsTheWindowClosed pins where the expiry check
// sits. A resolution can land after the attempt's deadline has already
// passed — the pool finished the teardown just as the window ran out —
// and reporting "runtime is not ready" then drops a usable Host on the
// floor: the caller refuses a request the workspace can serve, and only
// the next RPC gets the Host that was there all along.
func TestDoUsesAHostResolvedAsTheWindowClosed(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir(), "")
	r.window = 100 * time.Millisecond
	resolved := &host.Host{}
	r.ensureHost = func(attemptCtx context.Context, _ string) (*host.Host, error) {
		<-attemptCtx.Done()
		return resolved, nil
	}

	var calls int
	err := r.Do(context.Background(), "/workspace/a", nil,
		func(h *host.Host) error {
			calls++
			if h != resolved {
				t.Fatalf("fn got host %p, want %p", h, resolved)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Do = %v, want nil: the Host was resolved and usable", err)
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1", calls)
	}
}
