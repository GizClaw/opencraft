package core

import (
	"reflect"
	"testing"
)

// TestReloadWindowReloadsWhenNothingIsLive pins the success half of the
// truth table: no live run and a window to reload means the navigation is
// requested exactly once.
func TestReloadWindowReloadsWhenNothingIsLive(t *testing.T) {
	c := NewCore(t.TempDir(), t.TempDir(), t.TempDir())
	c.busyWorkspaces = func() []string { return nil }
	calls := 0
	c.reloadWindow = func() bool {
		calls++
		return true
	}

	out := c.ReloadWindow()
	if !out.Reloaded || out.Reason != "" || len(out.Busy) != 0 {
		t.Fatalf("outcome = %+v, want a reload with no reason", out)
	}
	if calls != 1 {
		t.Fatalf("reload calls = %d, want 1", calls)
	}
}

// TestReloadWindowRefusesWhileARunIsLive pins the half that guards the
// data: a live run's deltas are addressed to the page a reload would throw
// away, so the window keeps its document and the run is named.
func TestReloadWindowRefusesWhileARunIsLive(t *testing.T) {
	c := NewCore(t.TempDir(), t.TempDir(), t.TempDir())
	c.busyWorkspaces = func() []string { return []string{"/one", "/two"} }
	calls := 0
	c.reloadWindow = func() bool {
		calls++
		return true
	}

	out := c.ReloadWindow()
	if out.Reloaded {
		t.Fatal("ReloadWindow reloaded a window a live run was writing into")
	}
	if out.Reason != ReloadBusy {
		t.Fatalf("reason = %q, want %q", out.Reason, ReloadBusy)
	}
	if want := []string{"/one", "/two"}; !reflect.DeepEqual(out.Busy, want) {
		t.Fatalf("busy = %v, want %v", out.Busy, want)
	}
	if calls != 0 {
		t.Fatalf("reload calls = %d, want 0 while a run is live", calls)
	}
}

// TestReloadWindowWithoutWindow pins the nil-safe answer the frontend
// branches on: headless never attaches a window, and the desktop shell
// answers the same way before the entry point hands it one.
func TestReloadWindowWithoutWindow(t *testing.T) {
	c := NewCore(t.TempDir(), t.TempDir(), t.TempDir())
	c.busyWorkspaces = func() []string { return nil }

	out := c.ReloadWindow()
	if out.Reloaded || out.Reason != ReloadNoWindow {
		t.Fatalf("outcome = %+v, want the no-window refusal", out)
	}
}

// TestReloadWindowOnBareCore pins that a composition root built without
// the wiring (the stream tests build &Core{}) answers instead of panicking.
func TestReloadWindowOnBareCore(t *testing.T) {
	var c Core
	if out := c.ReloadWindow(); out.Reloaded || out.Reason != ReloadNoWindow {
		t.Fatalf("outcome = %+v, want the no-window refusal", out)
	}
	if busy := c.BusyWorkspaces(); len(busy) != 0 {
		t.Fatalf("BusyWorkspaces = %v, want none", busy)
	}
}
