package undo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/perf"
)

func benchUndoStore(tb testing.TB) *Store {
	tb.Helper()
	store := NewWithRoot(tb.TempDir(), tb.TempDir())
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		cid := "s-" + strings.Repeat(string(rune('a'+i%26)), 16)
		before := []FileState{state("a.go", true, strings.Repeat("x", 1024))}
		after := []FileState{state("a.go", true, strings.Repeat("y", 1024))}
		if _, err := store.Capture(ctx, cid, before, after); err != nil {
			tb.Fatal(err)
		}
	}
	return store
}

func BenchmarkPruneGlobal(b *testing.B) {
	store := benchUndoStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.pruneGlobal()
	}
}

func TestPruneGlobalWithinBudget(t *testing.T) {
	store := benchUndoStore(t)
	perf.AssertMedianWithin(t, 10, func() {
		store.pruneGlobal()
	}, 50*time.Millisecond)
}
