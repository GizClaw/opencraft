package sessions

import (
	"path/filepath"
	"testing"
)

func TestSessionModePersists(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}

	mode, err := store.Mode(id)
	if err != nil || mode != ModeWorkspace {
		t.Fatalf("fresh session mode = %q, %v; want workspace", mode, err)
	}
	if err := store.SetMode(id, ModeYOLO); err != nil {
		t.Fatal(err)
	}
	mode, err = store.Mode(id)
	if err != nil || mode != ModeYOLO {
		t.Fatalf("mode after set = %q, %v; want yolo", mode, err)
	}

	// A fresh store over the same root sees the persisted mode.
	reopened, err := New(store.root, 40)
	if err != nil {
		t.Fatal(err)
	}
	mode, err = reopened.Mode(id)
	if err != nil || mode != ModeYOLO {
		t.Fatalf("reopened mode = %q, %v; want yolo", mode, err)
	}
}

func TestSessionModeReadOnlyPersists(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMode(id, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	mode, err := store.Mode(id)
	if err != nil || mode != ModeReadOnly {
		t.Fatalf("mode after set = %q, %v; want read-only", mode, err)
	}
	if !mode.IsReadOnly() || mode.IsYOLO() {
		t.Fatalf("IsReadOnly/IsYOLO for %q = %v/%v", mode, mode.IsReadOnly(), mode.IsYOLO())
	}
}

func TestSessionModeIsolatedPerSession(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := store.Create()
	id2, _ := store.Create()
	if err := store.SetMode(id1, ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if mode, _ := store.Mode(id2); mode != ModeWorkspace {
		t.Fatalf("other session mode = %q, want workspace", mode)
	}
}

func TestSetModeRejectsUnknown(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := store.Create()
	if err := store.SetMode(id, Mode("lunatic")); err == nil {
		t.Fatal("unknown mode should error")
	}
}
