package memory

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
)

// TestArchiveObserverReleasesCompletedRuns pins the observer map's
// hygiene: the requests map lives for the whole Host generation, so a
// completed run — which this observer never archives, the committer
// owns it — must still drop its captured request, or every turn of
// every conversation leaves one *agent.Request behind.
func TestArchiveObserverReleasesCompletedRuns(t *testing.T) {
	obs, _, _ := newArchiveObserver(t)
	ctx := context.Background()

	completed := agent.Identity{
		RunID:          "run-done",
		AgentID:        "assistant",
		ConversationID: "s-1",
	}
	obs.OnRunStart(ctx, completed, &agent.Request{ContextID: "s-1"})
	obs.OnRunEnd(ctx, completed, &agent.Result{
		RunID:  "run-done",
		Status: agent.StatusCompleted,
	})

	// A run that ends without a result (the engine aborted) cleans up
	// the same way.
	aborted := agent.Identity{
		RunID:          "run-aborted",
		AgentID:        "assistant",
		ConversationID: "s-1",
	}
	obs.OnRunStart(ctx, aborted, &agent.Request{ContextID: "s-1"})
	obs.OnRunEnd(ctx, aborted, nil)

	if got := len(obs.requests); got != 0 {
		t.Fatalf("observer retained %d request(s) after run end, want 0", got)
	}
}
