package host

import (
	"errors"
	"testing"
	"time"
)

// The pool's assembly record is what a diagnostics panel reads off one
// target: how many assemblies ran, what asked for the last one, and —
// kept apart from it — the last refusal. "It works now" and "this is
// what was wrong" are different answers, and a page that has just
// started serving again is exactly when someone reads the second one.
func TestAssemblyRecordKeepsTheLastFailureApartFromTheLastAttempt(t *testing.T) {
	var m Manager
	target := AppTarget("hello")
	other := WorkspaceTarget("/tmp/host-assembly-other")

	if got := m.AssemblyStats(target); got != (AssemblyStats{}) {
		t.Fatalf("an unassembled target reports %+v, want the zero value", got)
	}
	if len(m.assemblies) != 0 {
		t.Fatalf("asking about a target recorded one: %+v", m.assemblies)
	}

	turn := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	reload := turn.Add(time.Minute)
	read := turn.Add(2 * time.Minute)
	refusal := errors.New(
		"layer.yaml: agents: app: engine: graph: open graph.yaml: no such file")

	m.recordAssembly(target, ReasonAppTurn, turn, nil)
	stats := m.AssemblyStats(target)
	if stats.Count != 1 {
		t.Fatalf("count after one assembly = %d, want 1", stats.Count)
	}
	if want := (AssemblyAttempt{Reason: ReasonAppTurn, At: turn}); stats.Last != want {
		t.Fatalf("last attempt = %+v, want %+v", stats.Last, want)
	}
	if stats.Last.Failed() {
		t.Fatalf("a successful attempt reports a failure: %+v", stats.Last)
	}
	if stats.LastFailure != (AssemblyAttempt{}) {
		t.Fatalf("a successful attempt recorded a refusal: %+v", stats.LastFailure)
	}

	m.recordAssembly(target, ReasonAppReload, reload, refusal)
	stats = m.AssemblyStats(target)
	if stats.Count != 1 {
		t.Fatalf("a refusal counted as an assembly: %+v", stats)
	}
	want := AssemblyAttempt{Reason: ReasonAppReload, At: reload, Err: refusal.Error()}
	if stats.Last != want {
		t.Fatalf("last attempt = %+v, want the refusal %+v", stats.Last, want)
	}
	if stats.LastFailure != want {
		t.Fatalf("last failure = %+v, want %+v", stats.LastFailure, want)
	}
	if !stats.LastFailure.Failed() {
		t.Fatalf("a refusal does not report itself as failed: %+v", stats.LastFailure)
	}

	// The application assembles again — the file was fixed — and the
	// record still holds what was wrong, with its own reason and moment.
	m.recordAssembly(target, ReasonAppRead, read, nil)
	stats = m.AssemblyStats(target)
	if stats.Count != 2 {
		t.Fatalf("count after the retry = %d, want 2", stats.Count)
	}
	if want := (AssemblyAttempt{Reason: ReasonAppRead, At: read}); stats.Last != want {
		t.Fatalf("last attempt = %+v, want %+v", stats.Last, want)
	}
	if want := (AssemblyAttempt{
		Reason: ReasonAppReload,
		At:     reload,
		Err:    refusal.Error(),
	}); stats.LastFailure != want {
		t.Fatalf("a later success erased the refusal: %+v", stats.LastFailure)
	}

	// One target's history is not another's: the pool keys its records
	// the way it keys everything else.
	m.recordAssembly(other, ReasonWorkspaceOpen, read, nil)
	if got := m.AssemblyStats(other); got.Count != 1 ||
		got.LastFailure != (AssemblyAttempt{}) {
		t.Fatalf("the other target's record = %+v", got)
	}
	if got := m.AssemblyStats(target); got.Count != 2 {
		t.Fatalf("recording another target changed this one: %+v", got)
	}
}
