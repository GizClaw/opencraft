package memory

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/sessions"
)

// stubSink records committed turns for the memory side.
type stubSink struct {
	mu    sync.Mutex
	turns []corememory.Turn
}

func (s *stubSink) CommitTurn(_ context.Context, turn corememory.Turn) error {
	s.mu.Lock()
	s.turns = append(s.turns, turn)
	s.mu.Unlock()
	return nil
}

func (s *stubSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.turns)
}

func newArchiveObserver(t *testing.T) (*archiveObserver, *sessions.Store, *stubSink) {
	t.Helper()
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}
	return &archiveObserver{
		store:    store,
		sink:     sink,
		settings: commitSettings{RuntimeID: "opencraft", AgentID: "assistant"},
		requests: make(map[string]*agent.Request),
	}, store, sink
}

func TestArchiveObserverArchivesCanceledTurn(t *testing.T) {
	obs, store, sink := newArchiveObserver(t)
	ctx := context.Background()
	id := agent.Identity{
		RunID:          "run-1",
		AgentID:        "assistant",
		ConversationID: "s-1",
	}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "写一篇 essay"),
	}
	obs.OnRunStart(ctx, id, req)
	obs.OnRunEnd(ctx, id, &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusCanceled,
		Messages: []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "Trees are the largest organisms…"},
			}},
		}},
	})

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history = %d messages, want 2", len(hist))
	}
	if hist[0].Role != message.RoleUser || hist[0].Content.Text() != "写一篇 essay" {
		t.Errorf("first message = %+v", hist[0])
	}
	if !strings.Contains(hist[1].Content.Text(), "Trees are the largest organisms") {
		t.Errorf("assistant partial missing: %+v", hist[1])
	}
	if sink.count() != 1 {
		t.Errorf("memory sink turns = %d, want 1", sink.count())
	}
}

// TestArchiveObserverKeepsIntermediateToolActivity verifies an
// interrupted turn persists its partial conversation from the final
// board: world-state sections excluded, tool-call and tool-result
// messages included.
func TestArchiveObserverKeepsIntermediateToolActivity(t *testing.T) {
	obs, store, sink := newArchiveObserver(t)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "部署一下"),
	}
	obs.OnRunStart(ctx, id, req)

	board := agent.NewBoard()
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "部署一下"))
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "execcommand", Arguments: []byte(`{"cmd":"go build"}`),
			}},
		}},
	})
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: "build ok",
			}},
		}},
	})
	board.SetVar("world.sections.count", int64(1))

	obs.OnRunEnd(ctx, id, &agent.Result{
		RunID:     "run-1",
		Status:    agent.StatusInterrupted,
		LastBoard: board,
	})

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("history = %d messages, want 3 (user, tool-call assistant, tool result)", len(hist))
	}
	if hist[0].Role != message.RoleUser || hist[0].Content.Text() != "部署一下" {
		t.Errorf("first message = %+v", hist[0])
	}
	if call, ok := hist[1].Content.Parts[0].(message.ToolCallPart); !ok ||
		call.Call.Name != "execcommand" {
		t.Errorf("tool-call assistant message = %+v, want structured ToolCallPart", hist[1])
	}
	if result, ok := hist[2].Content.Parts[0].(message.ToolResultPart); !ok ||
		result.Result.Content != "build ok" {
		t.Errorf("tool result message = %+v, want structured ToolResultPart", hist[2])
	}
	for _, m := range hist {
		if m.Role == message.RoleSystem {
			t.Fatalf("world-state section leaked into history: %+v", m)
		}
	}

	if sink.count() != 1 {
		t.Fatalf("memory sink turns = %d, want 1", sink.count())
	}
	if len(sink.turns[0].Messages) != 3 {
		t.Fatalf("sink messages = %d, want 3", len(sink.turns[0].Messages))
	}
}

func TestArchiveObserverSkipsCompletedTurn(t *testing.T) {
	obs, store, sink := newArchiveObserver(t)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "hi"),
	}
	obs.OnRunStart(ctx, id, req)
	obs.OnRunEnd(ctx, id, &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusCompleted,
		Messages: []message.Message{{
			Role:    message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{message.TextPart{Text: "ok"}}},
		}},
	})

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Fatalf("completed turn must not be archived by observer: %v", hist)
	}
	if sink.count() != 0 {
		t.Errorf("memory sink turns = %d, want 0", sink.count())
	}
}

func TestArchiveObserverSkipsRefereeAcceptedTurn(t *testing.T) {
	obs, store, sink := newArchiveObserver(t)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "hi"),
	}
	obs.OnRunStart(ctx, id, req)
	// A Referee accepted an interrupted turn: Committed is true, so
	// the committer owns it and the observer must stay out.
	obs.OnRunEnd(ctx, id, &agent.Result{
		RunID:     "run-1",
		Status:    agent.StatusInterrupted,
		Committed: true,
		Messages: []message.Message{{
			Role:    message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{message.TextPart{Text: "partial"}}},
		}},
	})

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Fatalf("referee-accepted turn must not be double-archived: %v", hist)
	}
	if sink.count() != 0 {
		t.Errorf("memory sink turns = %d, want 0", sink.count())
	}
}

func TestArchiveObserverWithoutOutputStillKeepsRequest(t *testing.T) {
	obs, store, sink := newArchiveObserver(t)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "问一个没人答的问题"),
	}
	obs.OnRunStart(ctx, id, req)
	obs.OnRunEnd(ctx, id, &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusFailed,
	})

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Role != message.RoleUser {
		t.Fatalf("history = %+v, want the user request only", hist)
	}
	if sink.count() != 0 {
		t.Errorf("memory sink turns = %d, want 0 (no produced messages)", sink.count())
	}
}

func TestArchiveObserverUnknownRunIgnored(t *testing.T) {
	obs, store, sink := newArchiveObserver(t)
	obs.OnRunEnd(context.Background(), agent.Identity{
		RunID:          "run-unknown",
		ConversationID: "s-1",
	}, &agent.Result{
		RunID:  "run-unknown",
		Status: agent.StatusCanceled,
	})

	hist, err := store.History(context.Background(), "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Fatalf("unknown run must not archive: %v", hist)
	}
	if sink.count() != 0 {
		t.Errorf("memory sink turns = %d, want 0", sink.count())
	}
}
