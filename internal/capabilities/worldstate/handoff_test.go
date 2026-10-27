package worldstate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// The replay handoff replaced a board var. These cases pin the three
// properties the world node depends on: the in-memory entry is what a
// rendered turn is merged from, it is consumed once (one merge per turn),
// and a board seeded by something else still finds its replay in the var.

// freshHandoff swaps the package registry for an empty one for the
// duration of the test, so cases cannot see each other's boards.
func freshHandoff(t *testing.T) {
	t.Helper()
	previous := pendingHistory
	pendingHistory = newHistoryHandoff(pendingHistoryDepth)
	t.Cleanup(func() { pendingHistory = previous })
}

func TestMergeTakesTheRenderedReplayFromTheHandoff(t *testing.T) {
	freshHandoff(t)
	board := agent.NewBoard()
	pendingHistory.put(board, []message.Message{
		message.NewTextMessage(message.RoleUser, "replayed user turn"),
		message.NewTextMessage(message.RoleAssistant, "replayed assistant turn"),
	})

	if _, err := Merge(board); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := board.Channel(agent.MainChannel)
	if len(got) != 2 {
		t.Fatalf("channel has %d messages, want the 2 replayed ones", len(got))
	}
	if text := got[0].Content.Text(); !strings.Contains(text, "replayed user turn") {
		t.Fatalf("channel[0] = %q, want the replayed user turn", text)
	}
	if raw := board.VarString(BoardVarHistory); raw != "" {
		t.Fatalf("%s is set (%d bytes), want no board var at all",
			BoardVarHistory, len(raw))
	}

	// One merge per turn: the second call must not replay the entry again.
	if _, err := Merge(board); err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if got := board.Channel(agent.MainChannel); len(got) != 2 {
		t.Fatalf("second merge left %d messages, want the channel unchanged", len(got))
	}
}

func TestMergeStillReadsAReplayFromTheBoardVar(t *testing.T) {
	freshHandoff(t)
	board := agent.NewBoard()
	board.SetVar(BoardVarHistory, `[{"role":"user","content":{"parts":`+
		`[{"type":"text","text":"replayed from the var"}]}}]`)

	if _, err := Merge(board); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := board.Channel(agent.MainChannel)
	if len(got) != 1 {
		t.Fatalf("channel has %d messages, want the 1 replayed one", len(got))
	}
	if text := got[0].Content.Text(); !strings.Contains(text, "replayed from the var") {
		t.Fatalf("channel[0] = %q, want the replayed message", text)
	}
}

func TestHistoryHandoffKeepsAtMostItsDepth(t *testing.T) {
	handoff := newHistoryHandoff(2)
	boards := make([]*agent.Board, 3)
	for i := range boards {
		boards[i] = agent.NewBoard()
		handoff.put(boards[i], []message.Message{
			message.NewTextMessage(message.RoleUser, fmt.Sprintf("turn %d", i)),
		})
	}
	if _, ok := handoff.take(boards[0]); ok {
		t.Fatal("the oldest entry survived the bound")
	}
	for _, board := range boards[1:] {
		if _, ok := handoff.take(board); !ok {
			t.Fatal("a retained entry was dropped")
		}
	}
}

func TestHistoryHandoffIgnoresAnEmptyReplay(t *testing.T) {
	handoff := newHistoryHandoff(2)
	board := agent.NewBoard()
	handoff.put(board, nil)
	if _, ok := handoff.take(board); ok {
		t.Fatal("an empty replay was recorded")
	}
}
