package worldstate

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// TestPublishCompactionBasePinsPrefix pins the pair the compaction node
// sizes a round against: the token estimate covers every section the world
// node prepends, and the length covers those sections plus the replayed
// history — the messages that sit in front of everything a round appends.
// A pair that disagrees with the channel it describes makes the node
// estimate a prefix it never sends (too long) or skip messages it does
// (too short), and neither shows up in a fold decision until the prompt is
// already over the window.
func TestPublishCompactionBasePinsPrefix(t *testing.T) {
	board := agent.NewBoard()
	env := newTextSection("environment", message.RoleUser, "workspace: /tmp/w")
	docs := newTextSection("agents_md", message.RoleUser, "run go test ./...")
	// An id the meter does not draw still rides the channel: the base is
	// the channel's prefix, not the meter's categories.
	unknown := newTextSection("something_new", message.RoleUser, "surprise")
	sections := []Section{env, docs, unknown}

	publishCompactionBase(board, sections, 5000, 3)

	wantTokens := summarytext.EstimateTokens(
		[]message.Message{env.Message, docs.Message, unknown.Message},
	) + 5000
	if got, _ := board.GetVar(config.BoardVarPromptBaseTokens); got != wantTokens {
		t.Fatalf("base tokens = %v, want %d (sections + history, one render)",
			got, wantTokens)
	}
	if got, _ := board.GetVar(config.BoardVarPromptBaseLen); got != len(sections)+3 {
		t.Fatalf("base len = %v, want %d (sections + replayed messages)",
			got, len(sections)+3)
	}
}

// TestPublishCompactionBaseClearsStalePair pins the reuse case: a board can
// outlive the turn that wrote it (a resume restores it), so a turn with no
// prefix to publish must delete the pair rather than leave the previous
// turn's shape standing. A stale base is worse than none — the node trusts
// it instead of measuring.
func TestPublishCompactionBaseClearsStalePair(t *testing.T) {
	board := agent.NewBoard()
	board.SetVar(config.BoardVarPromptBaseTokens, 1234)
	board.SetVar(config.BoardVarPromptBaseLen, 56)

	publishCompactionBase(board, nil, 0, 0)

	if _, ok := board.GetVar(config.BoardVarPromptBaseTokens); ok {
		t.Fatal("a turn with no sections left the previous base tokens standing")
	}
	if _, ok := board.GetVar(config.BoardVarPromptBaseLen); ok {
		t.Fatal("a turn with no sections left the previous base length standing")
	}
	// A nil board is a no-op, not a panic: prepare runs before the engine
	// owns one in some assemblies.
	publishCompactionBase(nil, nil, 0, 0)
}

// TestPublishCompactionBaseWithoutHistory covers bounded-memory turns: the
// prefix is the sections alone, and the base still describes exactly what
// the world node prepends.
func TestPublishCompactionBaseWithoutHistory(t *testing.T) {
	board := agent.NewBoard()
	docs := newTextSection("agents_md", message.RoleUser, "run go test ./...")

	publishCompactionBase(board, []Section{docs}, 0, 0)

	if got, _ := board.GetVar(config.BoardVarPromptBaseLen); got != 1 {
		t.Fatalf("base len = %v, want 1 (the single section)", got)
	}
	if got, _ := board.GetVar(config.BoardVarPromptBaseTokens); got !=
		summarytext.EstimateTokens([]message.Message{docs.Message}) {
		t.Fatalf("base tokens = %v, want the section's own estimate", got)
	}
}
