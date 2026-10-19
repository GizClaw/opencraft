package compaction

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/bindings"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// channelBoard builds a board whose MainChannel looks like a turn in
// progress: world sections, a replayed round, and this turn's tool
// traffic.
func channelBoard() *agent.Board {
	board := agent.NewBoard()
	board.SetChannel(agent.MainChannel, []message.Message{
		message.NewTextMessage(message.RoleSystem, "instructions"),
		message.NewTextMessage(message.RoleUser, "earlier question"),
		message.NewTextMessage(message.RoleAssistant, "earlier answer"),
		{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "read_file",
					Arguments: []byte(`{"path":"a.go"}`),
				}},
			}},
		},
		{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID:  "c1",
					Content: message.NewTextContent(strings.Repeat("file body ", 50)),
				}},
			}},
		},
	})
	return board
}

// TestEstimateChannelTokensReadsTheMainChannel pins the two things the
// compaction node relies on: the number is the shared summarytext
// estimate (not a second rule), and `from` addresses the channel the
// node already read — the measurement plus the tail it appended since.
func TestEstimateChannelTokensReadsTheMainChannel(t *testing.T) {
	board := channelBoard()
	whole := board.Channel(agent.MainChannel)

	got, err := EstimateChannelTokens(board, 0)
	if err != nil {
		t.Fatalf("estimate whole channel: %v", err)
	}
	if want := summarytext.EstimateTokens(whole); got != want {
		t.Fatalf("estimate(0) = %d, want %d", got, want)
	}

	got, err = EstimateChannelTokens(board, 4)
	if err != nil {
		t.Fatalf("estimate tail: %v", err)
	}
	if want := summarytext.EstimateTokens(whole[4:]); got != want {
		t.Fatalf("estimate(4) = %d, want %d", got, want)
	}
	if got >= summarytext.EstimateTokens(whole) {
		t.Fatalf("the tail estimate %d covers the whole channel; "+
			"the index was ignored", got)
	}

	// An empty tail is zero, not an error: the node asks about the slice
	// appended since a measurement, and there may be none yet.
	if got, err := EstimateChannelTokens(board, len(whole)); err != nil || got != 0 {
		t.Fatalf("estimate(len) = %d, %v; want 0, nil", got, err)
	}
}

// TestEstimateChannelTokensRejectsAnIndexOutsideTheChannel keeps a
// harness bug loud: the node only asks about slices it just read, so an
// index past the channel means the two disagree — and silently
// estimating a different slice would under-count the prompt the turn is
// about to send.
func TestEstimateChannelTokensRejectsAnIndexOutsideTheChannel(t *testing.T) {
	board := channelBoard()
	for _, from := range []int{-1, board.ChannelLen(agent.MainChannel) + 1} {
		if _, err := EstimateChannelTokens(board, from); err == nil {
			t.Fatalf("estimate(%d) accepted an out-of-range index", from)
		}
	}
	if _, err := EstimateChannelTokens(nil, 0); err == nil {
		t.Fatal("estimate accepted a nil board")
	}
}

// TestBridgeBindsOccompactEstimate pins the script surface: the node
// calls occompact.estimate, and that call is the same function — a
// rename here would break every round silently, because the node's
// ReferenceError only shows up at execution.
func TestBridgeBindsOccompactEstimate(t *testing.T) {
	board := channelBoard()
	list, err := Bridge{}.Bind(bindings.Invocation{
		Context: context.Background(), Board: board,
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if len(list) != 1 || list[0].Name != "occompact" {
		t.Fatalf("bindings = %+v, want one occompact global", list)
	}
	globals, ok := list[0].Value.(map[string]any)
	if !ok {
		t.Fatalf("occompact = %T, want a method bag", list[0].Value)
	}
	estimate, ok := globals["estimate"].(func(int) (int, error))
	if !ok {
		t.Fatalf("occompact.estimate = %T, want func(int) (int, error)",
			globals["estimate"])
	}
	got, err := estimate(0)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if want := summarytext.EstimateTokens(board.Channel(agent.MainChannel)); got != want {
		t.Fatalf("estimate(0) = %d, want %d", got, want)
	}
}
