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

	"github.com/GizClaw/opencraft/internal/capabilities/compaction"
)

// These benchmarks measure what one compaction round costs the process,
// through the same runtime the engine wires (jsrt: goja pool, board
// bridge, config, occompact). They exist because that cost used to be paid
// per round on the whole conversation: the node projected every message
// into script objects and walked every character to a token estimate, then
// threw the numbers away. A round now reads a count and the last message,
// and pays Go's estimate (capabilities/compaction) for the number.
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
	// counts and the model window (never the router in a benchmark).
	board.SetVar("world.sections.count", len(sections))
	board.SetVar("world.history.count", prefix-len(sections))
	board.SetVar("world.compact.max_input_tokens", 2000000)
	return board
}

func benchEnv(ctx context.Context, board *agent.Board) *agent.ScriptEnv {
	_, bridge := bindings.NewBoardBridge(board)(ctx)
	return &agent.ScriptEnv{
		Config: map[string]any{"retain_ratio": 0.16},
		Bindings: map[string]any{
			"board": bridge,
			"run": map[string]any{
				"get_context_id": func() string { return "bench" },
			},
			// The sizing call: the same function the node reaches in
			// production, bound to this board.
			"occompact": map[string]any{
				"estimate": func(from int) (int, error) {
					return compaction.EstimateChannelTokens(board, from)
				},
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

// BenchmarkCompactRound is one round of the node as it stands: a count, the
// last message, and one call into Go for the number.
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

// BenchmarkCompactRoundEstimate is the sizing call on its own: the Go
// estimate of the whole channel, out of the script interpreter. The
// difference against BenchmarkCompactRound is what a round pays besides it;
// the difference against the old per-character scan is what moving the rule
// to Go bought. It is also where the estimate's own cost lives: it used to
// render every message into a string and convert that to UTF-16 (13 MB of
// garbage a pass on this board), and now counts the parts instead
// (summarytext.EstimateTokens), which is why the number below is a fifth of
// what it was and allocates nothing.
func BenchmarkCompactRoundEstimate(b *testing.B) {
	board := benchBoard(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := compaction.EstimateChannelTokens(board, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompactRoundDoubleSizing adds the round's second sizing call,
// which the node makes when a measurement has to be topped up with the
// tokens appended since.
func BenchmarkCompactRoundDoubleSizing(b *testing.B) {
	board := benchBoard(b)
	rt, source := benchRuntime(b, board)
	env := benchEnv(context.Background(), board)
	source += "\n;estimateTokens(0);"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Exec(context.Background(), "compact", source, env); err != nil {
			b.Fatal(err)
		}
	}
}
