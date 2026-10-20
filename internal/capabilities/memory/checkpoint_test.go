package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// checkpointBoard builds the board a replay turn leaves behind: world
// sections, the whole replayed conversation, then this turn's messages,
// with the compact node having folded the older rounds of the turn onto
// the archive channel.
func checkpointBoard() *agent.Board {
	board := agent.NewBoard()
	board.SetVar(worldSectionsCountVar, 2)
	board.SetVar(worldHistoryCountVar, 3)
	board.SetVar("oc_thread_id", "oc-s-checkpoint")
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "memory summary section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, strings.Repeat("old question ", 400)))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, strings.Repeat("old answer ", 400)))
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID:  "old",
				Content: message.NewTextContent(strings.Repeat("old tool output ", 400)),
			}},
		}},
	})
	// The compact node moved the first round of this turn onto the
	// archive channel and left the turn's user message in place.
	board.AppendChannelMessage(config.CompactArchiveChannel, message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID:        "c1",
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"a.go"}`),
			}},
		}},
	})
	board.AppendChannelMessage(config.CompactArchiveChannel, message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID:  "c1",
				Content: message.NewTextContent("package a"),
			}},
		}},
	})
	board.SetVar("world.compact.turn_start", 5)
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleUser,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "fix the bug"},
			// The media prepare hook inlines attachment bytes before
			// the model sees the turn; the archive must not keep them,
			// so recovery re-anchors the request's own message.
			message.TextPart{Text: strings.Repeat("inlined-bytes ", 400)},
		}},
	})
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "looking at it"))
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleUser, summarytext.SummaryPrefix+"\nfolding so far"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "done"))
	return board
}

func checkpointRequest() *agent.Request {
	return &agent.Request{
		ContextID: "s-checkpoint",
		Message:   message.NewTextMessage(message.RoleUser, "fix the bug"),
	}
}

// TestTurnSnapshotDropsTheReplayPrefix pins the size side of the trim:
// with full-history replay on, every wave checkpoint used to re-serialize
// the whole replayed conversation even though recovery reads back only
// the turn.
func TestTurnSnapshotDropsTheReplayPrefix(t *testing.T) {
	board := agent.NewBoard()
	board.SetVar(worldSectionsCountVar, 1)
	board.SetVar(worldHistoryCountVar, 3)
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	replay := []message.Message{
		message.NewTextMessage(message.RoleUser, strings.Repeat("old question ", 3000)),
		message.NewTextMessage(message.RoleAssistant, strings.Repeat("old answer ", 3000)),
		message.NewTextMessage(message.RoleUser, strings.Repeat("older question ", 3000)),
	}
	for _, m := range replay {
		board.AppendChannelMessage(agent.MainChannel, m)
	}
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "and now this"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "on it"))

	req := &agent.Request{
		ContextID: "s-replay",
		Message:   message.NewTextMessage(message.RoleUser, "and now this"),
	}
	snap, ok := TurnSnapshot(board.Snapshot(), req)
	if !ok {
		t.Fatal("TurnSnapshot refused a replay board")
	}
	rawBoard, err := json.Marshal(board.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	rawTrimmed, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawTrimmed)*10 > len(rawBoard) {
		t.Fatalf("trimmed board = %d bytes, untrimmed = %d; want at least 10x smaller",
			len(rawTrimmed), len(rawBoard))
	}
	want := ExtractTurnMessages(board, req, nil)
	if got := ExtractTurnMessages(agent.RestoreBoard(snap), req, nil); !sameMessages(got, want) {
		t.Fatalf("extraction from the trimmed snapshot = %s, want %s",
			describeMessages(got), describeMessages(want))
	}
}

// TestTurnSnapshotKeepsOnlyTheTurn pins the checkpoint trim: the snapshot
// drops the world sections, the replayed prefix and the board vars a
// recovery never reads, while extracting the turn from it yields exactly
// what the untrimmed board yields — including the round the compact node
// folded onto the archive channel and the pre-inline form of the user
// message.
func TestTurnSnapshotKeepsOnlyTheTurn(t *testing.T) {
	board := checkpointBoard()
	req := checkpointRequest()

	want := ExtractTurnMessages(board, req, nil)
	snap, ok := TurnSnapshot(board.Snapshot(), req)
	if !ok {
		t.Fatal("TurnSnapshot refused a board that carries a turn")
	}

	if got := ExtractTurnMessages(agent.RestoreBoard(snap), req, nil); !sameMessages(got, want) {
		t.Fatalf("extraction from the trimmed snapshot = %s, want %s",
			describeMessages(got), describeMessages(want))
	}
	if len(snap.Channels) != 1 {
		t.Fatalf("trimmed channels = %v, want MainChannel only", channelNames(snap))
	}
	for name, msgs := range snap.Channels {
		if name != agent.MainChannel {
			t.Fatalf("trimmed snapshot kept channel %q with %d messages", name, len(msgs))
		}
		_ = msgs
	}
	if len(snap.Vars) != 2 {
		t.Fatalf("trimmed vars = %v, want only the two boundary counters", snap.Vars)
	}
	for _, name := range []string{worldSectionsCountVar, worldHistoryCountVar} {
		if got, ok := snap.Vars[name]; !ok || got != 0 {
			t.Fatalf("trimmed %s = %v (present %v), want 0", name, got, ok)
		}
	}
	for _, m := range snap.Channels[agent.MainChannel] {
		if m.Role == message.RoleSystem {
			t.Fatalf("world section survived the trim: %q", m.Content.Text())
		}
		if strings.HasPrefix(m.Content.Text(), summarytext.SummaryPrefix) {
			t.Fatalf("compaction summary survived the trim: %q", m.Content.Text())
		}
	}
	if got := snap.Channels[agent.MainChannel][0].Content.Text(); got != "fix the bug" {
		t.Fatalf("first trimmed message = %q, want the request's own text", got)
	}

	// Trimming a trimmed snapshot again is a fixpoint: recovery reads
	// the same turn, and nothing is left to drop.
	twice, ok := TurnSnapshot(snap, req)
	if !ok {
		t.Fatal("TurnSnapshot refused its own output")
	}
	if again := ExtractTurnMessages(agent.RestoreBoard(twice), req, nil); !sameMessages(again, want) {
		t.Fatalf("second trim changed the turn: %s", describeMessages(again))
	}
}

// TestTurnSnapshotRefusesBoardsWithoutATurn verifies the trim leaves
// alone what it cannot derive a turn from: a board whose world node has
// not run has no boundary marker, and dropping to the request-plus-tail
// fallback there would throw the turn's tool results away.
func TestTurnSnapshotRefusesBoardsWithoutATurn(t *testing.T) {
	if _, ok := TurnSnapshot(nil, checkpointRequest()); ok {
		t.Fatal("TurnSnapshot accepted a nil board")
	}

	noMarker := agent.NewBoard()
	noMarker.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "hello"))
	if _, ok := TurnSnapshot(noMarker.Snapshot(), checkpointRequest()); ok {
		t.Fatal("TurnSnapshot accepted a board without the world boundary marker")
	}

	// A board whose turn has not produced a message yet still trims: the
	// request-plus-tail fallback is exactly what recovery would compute
	// from the untrimmed board.
	early := agent.NewBoard()
	early.SetVar(worldSectionsCountVar, 1)
	early.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	req := checkpointRequest()
	snap, ok := TurnSnapshot(early.Snapshot(), req)
	if !ok {
		t.Fatal("TurnSnapshot refused a board whose turn just started")
	}
	want := ExtractTurnMessages(early, req, nil)
	if got := ExtractTurnMessages(agent.RestoreBoard(snap), req, nil); !sameMessages(got, want) {
		t.Fatalf("extraction from the trimmed snapshot = %s, want %s",
			describeMessages(got), describeMessages(want))
	}
}

// TestTurnSnapshotKeepsTheBoardWithoutARequest verifies a checkpoint
// written before the request annotation existed still trims: the turn's
// own user message (media inlined) is what recovery would persist anyway.
func TestTurnSnapshotKeepsTheBoardWithoutARequest(t *testing.T) {
	board := checkpointBoard()
	want := ExtractTurnMessages(board, nil, nil)

	snap, ok := TurnSnapshot(board.Snapshot(), nil)
	if !ok {
		t.Fatal("TurnSnapshot refused a board without a request")
	}
	if got := ExtractTurnMessages(agent.RestoreBoard(snap), nil, nil); !sameMessages(got, want) {
		t.Fatalf("extraction from the trimmed snapshot = %s, want %s",
			describeMessages(got), describeMessages(want))
	}
}

func sameMessages(a, b []message.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ra, err := json.Marshal(a[i])
		if err != nil {
			return false
		}
		rb, err := json.Marshal(b[i])
		if err != nil {
			return false
		}
		if string(ra) != string(rb) {
			return false
		}
	}
	return true
}

func describeMessages(msgs []message.Message) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		parts = append(parts, string(m.Role)+":"+m.Content.Text())
	}
	return "[" + strings.Join(parts, " | ") + "]"
}

func channelNames(snap *agent.BoardSnapshot) []string {
	names := make([]string, 0, len(snap.Channels))
	for name := range snap.Channels {
		names = append(names, name)
	}
	return names
}
