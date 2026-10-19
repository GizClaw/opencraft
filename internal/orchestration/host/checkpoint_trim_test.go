package host

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// trimFixtureBoard is the board a replay wave leaves on the checkpoint:
// world sections, the replayed conversation, then this turn's messages.
func trimFixtureBoard() *agent.Board {
	board := agent.NewBoard()
	board.SetVar("world.sections.count", 1)
	board.SetVar("world.history.count", 2)
	board.SetVar(boardVarThreadID, "oc-s-trim")
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, strings.Repeat("replayed question ", 2000)))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, strings.Repeat("replayed answer ", 2000)))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "this turn's ask"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "this turn's answer"))
	return board
}

func trimFixtureCheckpoint(conversationID, runID string) agent.Checkpoint {
	request := agent.Request{
		ContextID: conversationID,
		Message:   message.NewTextMessage(message.RoleUser, "this turn's ask"),
	}
	raw, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return agent.Checkpoint{
		ExecID:            runID,
		Steps:             []string{"world", "agent"},
		Iteration:         3,
		Board:             trimFixtureBoard().Snapshot(),
		Timestamp:         time.Now().UTC(),
		OriginalStartedAt: time.Now().Add(-time.Minute).UTC(),
		SpecVersion:       "assistant",
		Attributes: map[string]string{
			checkpointAttrConversationID: conversationID,
			checkpointAttrRequest:        string(raw),
		},
	}
}

// TestTrimCheckpointKeepsRecoveryEquivalent pins what a wave checkpoint
// has to keep: trimming must not change the turn recovery reconstructs
// from it, and it must keep every recovery annotation, including the
// board var fallback for the conversation id.
func TestTrimCheckpointKeepsRecoveryEquivalent(t *testing.T) {
	ctx := context.Background()
	cp := trimFixtureCheckpoint("s-trim", "run-trim00000001")

	fullBoard := cp.Board
	want := opmemory.ExtractTurnMessages(
		agent.RestoreBoard(fullBoard), checkpointRequest(ctx, &cp), nil)

	trimmed := trimCheckpoint(ctx, cp)

	if trimmed.ExecID != cp.ExecID || trimmed.Iteration != cp.Iteration ||
		len(trimmed.Steps) != len(cp.Steps) || trimmed.SpecVersion != cp.SpecVersion ||
		!trimmed.Timestamp.Equal(cp.Timestamp) ||
		!trimmed.OriginalStartedAt.Equal(cp.OriginalStartedAt) {
		t.Fatalf("trim changed the checkpoint envelope: %+v", trimmed)
	}
	if got := checkpointConversationID(&trimmed); got != "s-trim" {
		t.Fatalf("trimmed conversation id = %q, want s-trim", got)
	}
	// The board var is the fallback for checkpoints without the request
	// annotation; the trim keeps it.
	trimmed.Attributes = nil
	if got := checkpointConversationID(&trimmed); got != "s-trim" {
		t.Fatalf("trimmed board var fallback = %q, want s-trim", got)
	}

	got := opmemory.ExtractTurnMessages(
		agent.RestoreBoard(trimmed.Board), checkpointRequest(ctx, &cp), nil)
	if len(got) != len(want) {
		t.Fatalf("recovered %d messages from the trimmed board, want %d", len(got), len(want))
	}
	for i := range got {
		a, _ := json.Marshal(got[i])
		b, _ := json.Marshal(want[i])
		if string(a) != string(b) {
			t.Fatalf("message %d differs after the trim:\n got %s\nwant %s", i, a, b)
		}
	}

	// The point of the trim: the world sections and the replay prefix no
	// longer ride along on every wave.
	rawFull, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	rawTrimmed, err := json.Marshal(trimmed)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawTrimmed)*10 > len(rawFull) {
		t.Fatalf("trimmed checkpoint = %d bytes, untrimmed = %d; want at least 10x smaller",
			len(rawTrimmed), len(rawFull))
	}
	for _, m := range trimmed.Board.Channels[agent.MainChannel] {
		if m.Role == message.RoleSystem {
			t.Fatalf("world section survived the trim: %q", m.Content.Text())
		}
	}
}

// TestTrimCheckpointLeavesEarlyWavesAlone verifies a checkpoint whose
// world node has not run is persisted as handed over: there is no turn
// boundary to derive the slice from, and the board is nearly empty.
func TestTrimCheckpointLeavesEarlyWavesAlone(t *testing.T) {
	ctx := context.Background()
	board := agent.NewBoard()
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "hello"))
	cp := agent.Checkpoint{
		ExecID: "run-early00000001",
		Steps:  []string{"seed"},
		Board:  board.Snapshot(),
		Attributes: map[string]string{
			checkpointAttrConversationID: "s-early",
		},
	}

	trimmed := trimCheckpoint(ctx, cp)
	rawBefore, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	rawAfter, err := json.Marshal(trimmed)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawBefore) != string(rawAfter) {
		t.Fatalf("early-wave checkpoint changed:\n before %s\n after  %s", rawBefore, rawAfter)
	}
}

// TestTrimCheckpointRestoresTheArchiveChannel verifies the trim keeps
// the folded prefix: when the compact node has moved the turn's older
// rounds onto the archive channel, recovery rejoins them, so the trimmed
// board must carry them too.
func TestTrimCheckpointRestoresTheArchiveChannel(t *testing.T) {
	ctx := context.Background()
	cp := trimFixtureCheckpoint("s-fold", "run-fold000000001")
	// After the compact node folded this turn: the replayed history sits
	// at the front of the archive channel, then the round it folded, and
	// MainChannel keeps the sections, the ask and the newest round.
	board := agent.NewBoard()
	board.SetVar("world.sections.count", 1)
	board.SetVar("world.history.count", 2)
	board.SetVar("world.compact.turn_start", 1)
	board.SetVar(boardVarThreadID, "oc-s-fold")
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "this turn's ask"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "this turn's answer"))
	board.SetChannel(config.CompactArchiveChannel, []message.Message{
		message.NewTextMessage(message.RoleUser, "replayed question"),
		message.NewTextMessage(message.RoleAssistant, "replayed answer"),
		{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "read_file",
				Arguments: json.RawMessage(`{"path":"a.go"}`),
			}},
		}}},
		{Role: message.RoleTool, Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID:  "c1",
				Content: message.NewTextContent("package a"),
			}},
		}}},
	})
	cp.Board = board.Snapshot()

	trimmed := trimCheckpoint(ctx, cp)
	got := opmemory.ExtractTurnMessages(
		agent.RestoreBoard(trimmed.Board), checkpointRequest(ctx, &cp), nil)
	want := opmemory.ExtractTurnMessages(board, checkpointRequest(ctx, &cp), nil)
	if len(got) != len(want) {
		t.Fatalf("recovered %d messages from the trimmed board, want %d", len(got), len(want))
	}
	found := false
	for _, m := range got {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "package a") {
			found = true
		}
		if strings.Contains(string(raw), "replayed") {
			t.Fatalf("replayed history survived recovery: %s", raw)
		}
	}
	if !found {
		t.Fatal("the folded tool result is missing from the trimmed checkpoint")
	}
}
