package sessions

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/testing/perf"
)

func benchStore(tb testing.TB, turns int) (*Store, string) {
	tb.Helper()
	store, err := New(tb.TempDir(), 40)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = store.Close() })
	id, err := store.Create()
	if err != nil {
		tb.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < turns; i++ {
		msg := message.NewTextMessage(message.RoleUser, fmt.Sprintf("turn %d", i))
		if err := store.AppendTurnWithRunID(ctx, id, fmt.Sprintf("run-%d", i), []message.Message{msg}); err != nil {
			tb.Fatal(err)
		}
	}
	return store, id
}

func BenchmarkAppendTurnArtifactsIndexed(b *testing.B) {
	store, id := benchStore(b, 50)
	docs := []Artifact{{Path: "a.md", Bytes: 1}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.AppendTurnArtifacts(id, "run-1", docs); err != nil {
			b.Fatal(err)
		}
	}
}

// TestAppendTurnArtifactsWithinBudget is the absolute-threshold guard
// for the runs.json index: artifact reconciliation for a 50-turn
// session must stay cheap instead of re-reading every history file.
func TestAppendTurnArtifactsWithinBudget(t *testing.T) {
	store, id := benchStore(t, 50)
	docs := []Artifact{{Path: "a.md", Bytes: 1}}
	perf.AssertMedianWithin(t, 10, func() {
		if _, err := store.AppendTurnArtifacts(id, "run-1", docs); err != nil {
			t.Fatal(err)
		}
	}, 50*time.Millisecond)
}
