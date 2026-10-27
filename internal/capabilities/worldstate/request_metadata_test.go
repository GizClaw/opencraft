package worldstate

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
)

func TestSeedClientMetadata(t *testing.T) {
	board := agent.NewBoard()
	seedClientMetadata(board, agent.Identity{
		ConversationID: "s-abc",
		RunID:          "run-42",
	})
	if got := board.VarString(clientMetadataThreadVar); got != "oc-s-abc" {
		t.Fatalf("thread var = %q, want oc-s-abc", got)
	}
	if got := board.VarString(clientMetadataTurnVar); got != "oc-turn-run-42" {
		t.Fatalf("turn var = %q, want oc-turn-run-42", got)
	}
}

func TestSeedClientMetadataFallsBackToUnknown(t *testing.T) {
	board := agent.NewBoard()
	seedClientMetadata(board, agent.Identity{})
	if got := board.VarString(clientMetadataThreadVar); got != "oc-unknown" {
		t.Fatalf("thread var = %q, want oc-unknown", got)
	}
	if got := board.VarString(clientMetadataTurnVar); got != "oc-turn-unknown" {
		t.Fatalf("turn var = %q, want oc-turn-unknown", got)
	}
}
