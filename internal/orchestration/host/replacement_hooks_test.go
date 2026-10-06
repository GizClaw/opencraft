package host

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// waitForDeadline bounds the polls below: the deferred rebuild runs on
// its own goroutine, and every wait here is for that goroutine to settle
// on a target that assembles through a stub.
const waitForDeadline = 30 * time.Second

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(waitForDeadline)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestScheduleReplacementArmsOncePerTarget pins the arm contract the
// pool's one-watcher-per-drain policy rests on: the first call arms and
// reports true, a second call for the same target reports false because
// the drain is already accounted for, and another target has a slot of
// its own. One of the targets shares its id with a target in the other
// scope, because the slot is per target and two scopes must not wait on
// one arm. The Wanted hook blocks so the armed slots cannot be released
// underneath the assertions — the fast path disarms without assembling
// anything when it answers false.
func TestScheduleReplacementArmsOncePerTarget(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	release := make(chan struct{})
	var mu sync.Mutex
	var watched []Target
	m.SetReplacementHooks(ReplacementHooks{
		Wanted: func(t Target) bool {
			mu.Lock()
			watched = append(watched, t)
			mu.Unlock()
			<-release
			return false
		},
	})
	targets := []Target{
		WorkspaceTarget("/workspace/a"),
		AppTarget("/workspace/a"),
		WorkspaceTarget("/workspace/b"),
	}
	for _, target := range targets {
		if !m.ScheduleReplacement(context.Background(), target) {
			t.Fatalf("the first arm for %s was refused", target)
		}
		if m.ScheduleReplacement(context.Background(), target) {
			t.Fatalf("a second arm for %s reported true", target)
		}
		if !m.ReplacementArmed(target) {
			t.Fatalf("%s is not armed", target)
		}
	}
	// An unnamed target is never armed, and consumes no slot.
	if m.ScheduleReplacement(context.Background(), Target{}) {
		t.Fatal("an unnamed target was armed")
	}
	if m.ReplacementArmed(Target{}) {
		t.Fatal("an unnamed target reports itself as armed")
	}
	for _, target := range targets {
		if !m.ReplacementArmed(target) {
			t.Fatalf("the unnamed arm disturbed %s's slot", target)
		}
	}

	close(release)
	waitFor(t, func() bool {
		for _, target := range targets {
			if m.ReplacementArmed(target) {
				return false
			}
		}
		return true
	}, "the armed slots were never released")

	mu.Lock()
	asked := len(watched)
	mu.Unlock()
	if asked != len(targets) {
		t.Fatalf("Wanted was asked %d times, want once per armed target", asked)
	}
}

// TestReplacementWantedRefusalLeavesTheTargetUnserved pins the policy
// half of the deferred rebuild: an adapter that answers "the window has
// left this workspace" keeps the pool from assembling a runtime nobody
// is going to look at — and the armed slot is released all the same, so
// a later reload can arm a replacement of its own.
func TestReplacementWantedRefusalLeavesTheTargetUnserved(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	var builds int
	m.assembleHost = func(
		_ context.Context, t Target, _ interact.Backend, _ func(string) interact.Backend,
	) (*Host, error) {
		builds++
		return fakeManagerHost(m, t, 0), nil
	}
	var asked []Target
	m.SetReplacementHooks(ReplacementHooks{
		Wanted: func(t Target) bool {
			asked = append(asked, t)
			return false
		},
	})
	target := WorkspaceTarget("/workspace/left")

	if !m.ScheduleReplacement(context.Background(), target) {
		t.Fatal("the replacement was not armed")
	}
	waitFor(t, func() bool { return !m.ReplacementArmed(target) },
		"the armed slot was never released")

	// The observation above is mutex-ordered after the watcher settled,
	// so these reads see whatever it did.
	if builds != 0 {
		t.Fatalf("the assembler ran %d times for a target nobody wants", builds)
	}
	if got := m.Current(target); got != nil {
		t.Fatalf("Current = %p, want the refused target unserved", got)
	}
	if len(asked) != 1 || asked[0] != target {
		t.Fatalf("Wanted was asked for %v, want exactly %s", asked, target)
	}
}

// TestReplacementInstallsOnePerTarget pins the notification half and the
// per-target bookkeeping together: every target whose drain settles gets
// its own replacement assembled, pooled under its own target, and
// announced — the adapter's cue to refresh what it renders.
func TestReplacementInstallsOnePerTarget(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	m.assembleHost = func(
		_ context.Context, t Target, _ interact.Backend, _ func(string) interact.Backend,
	) (*Host, error) {
		return fakeManagerHost(m, t, 0), nil
	}
	installed := make(chan Target, 4)
	m.SetReplacementHooks(ReplacementHooks{
		Installed: func(t Target) { installed <- t },
	})
	a, b := WorkspaceTarget("/workspace/a"), WorkspaceTarget("/workspace/b")
	for _, target := range []Target{a, b} {
		if !m.ScheduleReplacement(context.Background(), target) {
			t.Fatalf("the replacement for %s was not armed", target)
		}
	}

	seen := make(map[Target]bool)
	for len(seen) < 2 {
		select {
		case got := <-installed:
			seen[got] = true
		case <-time.After(waitForDeadline):
			t.Fatalf("installed = %v, want both targets", seen)
		}
	}
	for _, target := range []Target{a, b} {
		h := m.Current(target)
		if h == nil {
			t.Fatalf("%s has no replacement after its install notice", target)
		}
		if h.Target() != target {
			t.Fatalf("replacement for %s serves %s", target, h.Target())
		}
		if h.AppID() != "" {
			t.Fatalf("workspace replacement reports app id %q", h.AppID())
		}
	}
}
