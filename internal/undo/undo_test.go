package undo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func state(path string, present bool, content string) FileState {
	return FileState{Path: path, Present: present, Content: content}
}

func readFile(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data), true
}

func TestUndoRedoRoundTrip(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	ctx := context.Background()
	const cid = "s-abcdef0123456789"

	// Turn 1: modify a.go, add b.go.
	before1 := []FileState{
		state("a.go", true, "v1"),
		state("b.go", false, ""),
	}
	after1 := []FileState{
		state("a.go", true, "v2"),
		state("b.go", true, "new"),
	}
	if _, err := store.Capture(ctx, cid, before1, after1); err != nil {
		t.Fatalf("capture 1: %v", err)
	}
	// Turn 2: delete b.go.
	before2 := []FileState{state("b.go", true, "new")}
	after2 := []FileState{state("b.go", false, "")}
	if _, err := store.Capture(ctx, cid, before2, after2); err != nil {
		t.Fatalf("capture 2: %v", err)
	}

	// Apply turn-2 before + turn-1 after to mirror the workspace.
	if _, err := store.apply(before2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.apply(after1); err != nil {
		t.Fatal(err)
	}

	// Undo turn 2: b.go comes back.
	changed, err := store.Undo(ctx, cid)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if len(changed) != 1 || changed[0] != "b.go" {
		t.Fatalf("undo changed = %v, want [b.go]", changed)
	}
	if got, ok := readFile(t, root, "b.go"); !ok || got != "new" {
		t.Fatalf("after undo b.go = %q, %v", got, ok)
	}

	// Redo turn 2: b.go disappears again.
	changed, err = store.Redo(ctx, cid)
	if err != nil {
		t.Fatalf("redo: %v", err)
	}
	if _, ok := readFile(t, root, "b.go"); ok {
		t.Fatal("after redo b.go still exists")
	}
	if len(changed) != 1 || changed[0] != "b.go" {
		t.Fatalf("redo changed = %v", changed)
	}
}

func TestGlobalPruneBoundsDiskUsage(t *testing.T) {
	old := maxUndoBytes
	maxUndoBytes = 1 << 10
	t.Cleanup(func() { maxUndoBytes = old })

	root := t.TempDir()
	store := New(root)
	ctx := context.Background()
	for _, cid := range []string{"s-aaaaaaaaaaaaaaaa", "s-bbbbbbbbbbbbbbbb"} {
		for i := 0; i < 3; i++ {
			before := []FileState{state("a.go", true, strings.Repeat("x", 512))}
			after := []FileState{state("a.go", true, strings.Repeat("y", 512))}
			if _, err := store.Capture(ctx, cid, before, after); err != nil {
				t.Fatalf("capture %s/%d: %v", cid, i, err)
			}
		}
	}

	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	// At most the budget plus the newest kept entry (which is exempt).
	if total > maxUndoBytes+2<<10 {
		t.Fatalf("undo total = %d bytes, want <= %d", total, maxUndoBytes+2<<10)
	}
	if _, err := store.Available(ctx, "s-aaaaaaaaaaaaaaaa"); err != nil {
		t.Fatalf("newest entry must survive pruning: %v", err)
	}

	// Snapshot dirs are owner-only.
	undoRoot := filepath.Join(root, ".opencraft", "undo")
	for _, sub := range []string{"live", "undone"} {
		info, err := os.Stat(filepath.Join(undoRoot, "s-aaaaaaaaaaaaaaaa", sub))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("%s mode = %o, want 700", sub, perm)
		}
	}
}

func TestUndoRestoresDeletedAndNewFiles(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	ctx := context.Background()
	const cid = "s-deadbeef"

	// A turn that deletes c.txt and creates d.txt.
	if _, err := store.Capture(ctx, cid,
		[]FileState{state("c.txt", true, "old"), state("d.txt", false, "")},
		[]FileState{state("c.txt", false, ""), state("d.txt", true, "fresh")},
	); err != nil {
		t.Fatal(err)
	}
	// Mirror the after state.
	if _, err := store.apply([]FileState{
		state("c.txt", false, ""), state("d.txt", true, "fresh"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Undo(ctx, cid); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got, ok := readFile(t, root, "c.txt"); !ok || got != "old" {
		t.Fatalf("c.txt = %q, %v", got, ok)
	}
	if _, ok := readFile(t, root, "d.txt"); ok {
		t.Fatal("d.txt should be removed by undo")
	}
}

func TestCaptureDropsIdenticalPair(t *testing.T) {
	store := New(t.TempDir())
	st := []FileState{state("a.go", true, "same")}
	seq, err := store.Capture(context.Background(), "s-1", st, st)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("identical capture returned seq %d, want 0", seq)
	}
	avail, err := store.Available(context.Background(), "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if avail.CanUndo || avail.CanRedo {
		t.Fatalf("empty store must report no undo/redo: %+v", avail)
	}
}

func TestNewCaptureClearsRedo(t *testing.T) {
	store := New(t.TempDir())
	ctx := context.Background()
	const cid = "s-2"
	if _, err := store.Capture(ctx, cid,
		[]FileState{state("a", true, "1")},
		[]FileState{state("a", true, "2")},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Undo(ctx, cid); err != nil {
		t.Fatalf("undo: %v", err)
	}
	avail, _ := store.Available(ctx, cid)
	if !avail.CanRedo {
		t.Fatal("redo should be available after undo")
	}
	if _, err := store.Capture(ctx, cid,
		[]FileState{state("b", true, "x")},
		[]FileState{state("b", true, "y")},
	); err != nil {
		t.Fatal(err)
	}
	avail, _ = store.Available(ctx, cid)
	if avail.CanRedo {
		t.Fatal("new capture must clear the redo stack")
	}
}

func TestUndoEmpty(t *testing.T) {
	store := New(t.TempDir())
	if _, err := store.Undo(context.Background(), "s-3"); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Undo on empty store = %v, want ErrEmpty", err)
	}
}

func TestApplyRejectsEscapingPath(t *testing.T) {
	store := New(t.TempDir())
	if _, err := store.apply([]FileState{state("../evil", true, "x")}); err == nil {
		t.Fatal("escaping path must be rejected")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	ctx := context.Background()
	const cid = "s-4"
	for i := 0; i < maxLiveEntries+5; i++ {
		before := []FileState{state("a", true, "b")}
		after := []FileState{state("a", true, fmt.Sprintf("v%d", i))}
		if _, err := store.Capture(ctx, cid, before, after); err != nil {
			t.Fatal(err)
		}
	}
	live, undone, err := store.dirs(cid)
	if err != nil {
		t.Fatal(err)
	}
	seqs, err := listSeqs(live)
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != maxLiveEntries {
		t.Fatalf("live entries = %d, want %d", len(seqs), maxLiveEntries)
	}
	if seqs[0] != 6 {
		t.Fatalf("oldest kept seq = %d, want 6", seqs[0])
	}
	_ = undone
}
