package config

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/bindings"
	"github.com/GizClaw/flowcraft/core/agent/scriptrt/jsrt"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// These benchmarks measure what one compaction round costs the process,
// through the same runtime the engine wires (jsrt: goja pool, board
// bridge, config). They exist because that cost used to be paid per round
// on the whole conversation: the node projected every message into script
// objects and rendered the whole prompt to a character estimate, then
// threw the numbers away. The narrow reads (channelLen / channelTail) and
// the prepare-time base are what a round costs now.
//
// Run with:
//
//	go test ./internal/foundation/config/ -run '^$' -bench CompactRound -benchmem
//
// The board is sized like a long replayed conversation: ~3 MB of text
// across 1200 messages, which is the shape the fold decision has to be
// right about and the shape rendering it is expensive for.
const (
	benchMessages  = 1200
	benchTextChars = 2500
)

func benchBoard(b *testing.B) *agent.Board {
	b.Helper()
	board := agent.NewBoard()
	msgs := make([]message.Message, 0, benchMessages)
	// World sections: the stable prefix the prepare hook renders.
	sections := []string{"rules", "environment", "permissions", "AGENTS.md"}
	for _, id := range sections {
		msgs = append(msgs, message.NewTextMessage(message.RoleSystem,
			id+": "+strings.Repeat("s", 400)))
	}
	// Replayed history, in the turn/answer pairs a real transcript has,
	// with the occasional tool result of a realistic size.
	body := strings.Repeat("lorem ipsum dolor sit amet ", benchTextChars/27+1)
	for i := 0; len(msgs) < benchMessages-3; i++ {
		if i%3 == 2 {
			msgs = append(msgs, message.NewTextMessage(message.RoleTool, body))
			continue
		}
		msgs = append(msgs, message.NewTextMessage(
			message.RoleUser, fmt.Sprintf("turn %d: %s", i, body[:benchTextChars/2])))
		msgs = append(msgs, message.NewTextMessage(
			message.RoleAssistant, fmt.Sprintf("answer %d: %s", i, body[:benchTextChars/2])))
	}
	prefix := len(msgs)
	msgs = append(msgs, message.NewTextMessage(message.RoleUser, "the current ask"))
	msgs = append(msgs, message.NewTextMessage(message.RoleAssistant, "working on it"))
	msgs = append(msgs, message.NewTextMessage(message.RoleTool, "ok"))
	board.SetChannel(agent.MainChannel, msgs)

	// What the world node and the prepare hook stamp for a turn: the
	// counts, the model window (never the router in a benchmark), and the
	// base pair the node sizes against.
	board.SetVar("world.sections.count", len(sections))
	board.SetVar("world.history.count", prefix-len(sections))
	board.SetVar("world.compact.max_input_tokens", 2000000)
	board.SetVar(BoardVarPromptBaseLen, prefix)
	board.SetVar(BoardVarPromptBaseTokens, summarytext.EstimateTokens(msgs[:prefix]))
	return board
}

func benchEnv(ctx context.Context, board *agent.Board) *agent.ScriptEnv {
	_, bridge := bindings.NewBoardBridge(board)(ctx)
	return &agent.ScriptEnv{
		Config: map[string]any{"preserve_recent": 10},
		Bindings: map[string]any{
			"board": bridge,
			"run": map[string]any{
				"get_context_id": func() string { return "bench" },
			},
			"inference": map[string]any{
				"routeExplain": func(map[string]any) map[string]any { return nil },
			},
		},
	}
}

func benchRuntime(b *testing.B, board *agent.Board) (*jsrt.Runtime, string) {
	b.Helper()
	data, err := FS().ReadFile("assets/graphs/nodes/compact.js")
	if err != nil {
		b.Fatal(err)
	}
	source := string(data)
	return jsrt.New(), source
}

// BenchmarkCompactRound is one round of the node as it stands: counts and
// tails only, with the prepare-time base covering the conversation prefix.
func BenchmarkCompactRound(b *testing.B) {
	board := benchBoard(b)
	rt, source := benchRuntime(b, board)
	env := benchEnv(context.Background(), board)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Exec(context.Background(), "compact", source, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompactRoundProjection is the read the node used to make at
// load time: the whole channel projected into script objects, every
// round, whether or not anything was over budget.
func BenchmarkCompactRoundProjection(b *testing.B) {
	board := benchBoard(b)
	rt, _ := benchRuntime(b, board)
	env := benchEnv(context.Background(), board)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Exec(context.Background(), "compact",
			"board.channel(board.MAIN_CHANNEL);", env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompactRoundWithFullSizing adds the old estimate on top of the
// current round: project the channel, render every message, scan every
// character. The difference against BenchmarkCompactRound is what the
// prepare-time base removed from a round that folds nothing.
func BenchmarkCompactRoundWithFullSizing(b *testing.B) {
	board := benchBoard(b)
	rt, source := benchRuntime(b, board)
	env := benchEnv(context.Background(), board)
	source += "\n;estimateTokens(board.channel(board.MAIN_CHANNEL));"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Exec(context.Background(), "compact", source, env); err != nil {
			b.Fatal(err)
		}
	}
}
