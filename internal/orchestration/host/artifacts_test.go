package host

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
)

// TestArtifactConversationSkipsEphemeralRuns pins the guard against the
// warning a delegated subagent used to produce on every file it wrote: the
// session store only owns "s-" conversations, so a "ctx-" run buffers
// nothing instead of reporting a rejected store call.
func TestArtifactConversationSkipsEphemeralRuns(t *testing.T) {
	ctx := func(id string) context.Context {
		return agent.WithRunInfo(context.Background(), agent.RunInfo{
			Identity: agent.Identity{ConversationID: id},
		})
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"session", ctx("s-6dae2e9441f8fe52"), "s-6dae2e9441f8fe52"},
		{"delegated subagent", ctx("ctx-4abfc8f0357c6e5d51edbc0b"), ""},
		{"no run info", context.Background(), ""},
		{"empty id", ctx(""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := artifactConversation(tc.ctx); got != tc.want {
				t.Fatalf("conversation = %q, want %q", got, tc.want)
			}
		})
	}
}
