package sessions

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/profile"
)

// freshSessionMode is the mode a newly created session resolves to in
// the current build profile (yolo in yoloonly builds, workspace
// otherwise).
func freshSessionMode() Mode {
	if profile.YoloOnly() {
		return ModeYOLO
	}
	return ModeWorkspace
}

func TestSessionModePersists(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}

	mode, err := store.Mode(context.Background(), id)
	if err != nil || mode != freshSessionMode() {
		t.Fatalf("fresh session mode = %q, %v; want %q", mode, err, freshSessionMode())
	}
	if err := store.SetMode(context.Background(), id, ModeYOLO); err != nil {
		t.Fatal(err)
	}
	mode, err = store.Mode(context.Background(), id)
	if err != nil || mode != ModeYOLO {
		t.Fatalf("mode after set = %q, %v; want yolo", mode, err)
	}

	// A fresh store over the same root sees the persisted mode.
	reopened, err := newMigratedStore(store.root, 40)
	if err != nil {
		t.Fatal(err)
	}
	mode, err = reopened.Mode(context.Background(), id)
	if err != nil || mode != ModeYOLO {
		t.Fatalf("reopened mode = %q, %v; want yolo", mode, err)
	}
}

func TestSessionModeReadOnlyPersists(t *testing.T) {
	if profile.YoloOnly() {
		t.Skip("read-only sessions do not exist in the yoloonly build")
	}
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMode(context.Background(), id, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	mode, err := store.Mode(context.Background(), id)
	if err != nil || mode != ModeReadOnly {
		t.Fatalf("mode after set = %q, %v; want read-only", mode, err)
	}
	if !mode.IsReadOnly() || mode.IsYOLO() {
		t.Fatalf("IsReadOnly/IsYOLO for %q = %v/%v", mode, mode.IsReadOnly(), mode.IsYOLO())
	}
}

func TestSessionModeIsolatedPerSession(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := store.Create()
	id2, _ := store.Create()
	if err := store.SetMode(context.Background(), id1, ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if mode, _ := store.Mode(context.Background(), id2); mode != freshSessionMode() {
		t.Fatalf("other session mode = %q, want %q", mode, freshSessionMode())
	}
}

func TestSetModeRejectsUnknown(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := store.Create()
	if err := store.SetMode(context.Background(), id, Mode("lunatic")); err == nil {
		t.Fatal("unknown mode should error")
	}
}
