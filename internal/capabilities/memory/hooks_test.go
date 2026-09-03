package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// TestCommitHookAtomicMemory verifies the production memory resource
// appends archive and memory rows inside one transaction: both tables
// are written by the same committer invocation.
func TestCommitHookAtomicMemory(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Database().Migrate(
		context.Background(), Migrations(),
	); err != nil {
		t.Fatal(err)
	}
	adapter := &sqliteTurnStore{db: store.Database()}
	res := &memoryResource{
		Assembly: summary.NewAssembly(adapter),
		store:    adapter,
	}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"memory":   res,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	committer := value.(agent.CommitterFunc)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-atomic", AgentID: "assistant", ConversationID: "s-atomic"}
	req := &agent.Request{
		ContextID: "s-atomic",
		Message:   message.NewTextMessage(message.RoleUser, "hi"),
	}
	resp := &agent.Result{
		RunID:  "run-atomic",
		Status: agent.StatusCompleted,
		Messages: []message.Message{
			message.NewTextMessage(message.RoleAssistant, "hello"),
		},
	}
	if err := committer(ctx, id, req, resp); err != nil {
		t.Fatal(err)
	}
	if n, err := adapter.CountMessages(ctx, "s-atomic"); err != nil || n != 2 {
		t.Fatalf("memory messages = %d, %v; want 2", n, err)
	}
	hist, err := store.History(ctx, "s-atomic", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("archive history = %d, want 2", len(hist))
	}
}

// TestCommitHookFactoryCommitsTurn verifies the opencraft.commit committer:
// it writes request+result to the session archive and one turn to the memory
// sink carrying the configured scope, the run id as idempotency key, and the
// conversation id.
func TestCommitHookFactoryCommitsTurn(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"runtime_id":"rt-1","user_id":"u-9","agent_id":"assistant"}`),
		Deps: map[string]any{
			"memory":   sink,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatalf("factory New: %v", err)
	}
	committer, ok := value.(agent.CommitterFunc)
	if !ok {
		t.Fatalf("value = %T, want agent.CommitterFunc", value)
	}

	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "实现一个缓存"),
	}
	res := &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusCompleted,
		Messages: []message.Message{
			message.NewTextMessage(message.RoleAssistant, "好的，已实现"),
		},
	}
	if err := committer(ctx, id, req, res); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Session archive keeps both sides of the turn.
	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history = %d messages, want 2 (request + result)", len(hist))
	}
	if hist[0].Role != message.RoleUser || hist[0].Content.Text() != "实现一个缓存" {
		t.Errorf("first message = %+v", hist[0])
	}
	if hist[1].Role != message.RoleAssistant || hist[1].Content.Text() != "好的，已实现" {
		t.Errorf("second message = %+v", hist[1])
	}

	// Memory sink got exactly one turn with the configured scope and
	// the run id as idempotency key.
	if sink.count() != 1 {
		t.Fatalf("memory sink turns = %d, want 1", sink.count())
	}
	turn := sink.turns[0]
	if turn.Scope.RuntimeID != "rt-1" || turn.Scope.UserID != "u-9" || turn.Scope.AgentID != "assistant" {
		t.Errorf("scope = %+v, want rt-1/u-9/assistant", turn.Scope)
	}
	if turn.ConversationID != "s-1" {
		t.Errorf("conversation id = %q, want s-1", turn.ConversationID)
	}
	if turn.IdempotencyKey != "run-1" {
		t.Errorf("idempotency key = %q, want run-1", turn.IdempotencyKey)
	}
	if len(turn.Messages) != 2 ||
		turn.Messages[0].Role != message.RoleUser ||
		turn.Messages[0].Content.Text() != "实现一个缓存" ||
		turn.Messages[1].Role != message.RoleAssistant ||
		turn.Messages[1].Content.Text() != "好的，已实现" {
		t.Errorf("sink messages = %+v, want user request then final assistant", turn.Messages)
	}
}

// TestCommitHookSkipsEmptyResult verifies a turn that produced no messages
// writes nothing: neither the archive (request alone would be noise) nor the
// memory sink (CommitTurn requires at least one message).
func TestCommitHookSkipsEmptyResult(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"memory":   sink,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	committer := value.(agent.CommitterFunc)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", ConversationID: "s-1"}

	if err := committer(ctx, id, &agent.Request{ContextID: "s-1"}, &agent.Result{RunID: "run-1"}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Fatalf("history = %d messages, want 0", len(hist))
	}
	if sink.count() != 0 {
		t.Fatalf("memory sink turns = %d, want 0", sink.count())
	}
}

// TestCommitHookSkipsEphemeralContext verifies that a completed
// delegated subagent run (fresh "ctx-" ContextID, no persisted
// conversation) is not archived and does not fail the run: the session
// store only accepts "s-" conversation ids, so the committer must skip
// ephemeral contexts instead of returning "sessions: invalid session id".
func TestCommitHookSkipsEphemeralContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := sessions.New(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"memory":   sink,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	committer := value.(agent.CommitterFunc)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: "ctx-abc"}

	err = committer(ctx, id, &agent.Request{
		ContextID: "ctx-abc",
		Message:   message.NewTextMessage(message.RoleUser, "做个子任务"),
	}, &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusCompleted,
		Messages: []message.Message{
			message.NewTextMessage(message.RoleAssistant, "子任务完成"),
		},
	})
	if err != nil {
		t.Fatalf("commit of ephemeral context must not fail: %v", err)
	}
	if sink.count() != 0 {
		t.Fatalf("memory sink turns = %d, want 0 for ephemeral context", sink.count())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "ctx-") {
			t.Fatalf("ephemeral context was archived: found dir %s", e.Name())
		}
	}
}

// TestCommitHookPersistsFullConversation verifies that when the final
// board carries the world-state section marker, the committer archives
// and commits the whole turn conversation — user request, assistant
// tool-call round, tool result, final reply — while excluding the
// injected context sections, with tool activity rendered as text.
func TestCommitHookPersistsFullConversation(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"memory":   sink,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatalf("factory New: %v", err)
	}
	committer := value.(agent.CommitterFunc)

	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "查一下天气"),
	}

	board := agent.NewBoard()
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "memory summary section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "查一下天气"))
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "webfetch",
				Arguments: json.RawMessage(`{"url":"https://example.com"}`),
			}},
		}},
	})
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: "sunny 28C",
			}},
		}},
	})
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "今天 28 度"))
	board.SetVar("world.sections.count", int64(2))

	res := &agent.Result{
		RunID:     "run-1",
		Status:    agent.StatusCompleted,
		LastBoard: board,
		Messages: []message.Message{
			message.NewTextMessage(message.RoleAssistant, "今天 28 度"),
		},
	}
	if err := committer(ctx, id, req, res); err != nil {
		t.Fatalf("commit: %v", err)
	}

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 4 {
		t.Fatalf("history = %d messages, want 4 (user, tool-call assistant, tool result, final)", len(hist))
	}
	if hist[0].Role != message.RoleUser || hist[0].Content.Text() != "查一下天气" {
		t.Errorf("first message = %+v", hist[0])
	}
	if hist[1].Role != message.RoleAssistant {
		t.Errorf("second message = %+v", hist[1])
	} else if call, ok := hist[1].Content.Parts[0].(message.ToolCallPart); !ok ||
		call.Call.Name != "webfetch" {
		t.Errorf("tool-call assistant message = %+v, want structured ToolCallPart", hist[1])
	}
	if hist[2].Role != message.RoleTool {
		t.Errorf("third message = %+v", hist[2])
	} else if result, ok := hist[2].Content.Parts[0].(message.ToolResultPart); !ok ||
		result.Result.Content != "sunny 28C" {
		t.Errorf("tool result message = %+v, want structured ToolResultPart", hist[2])
	}
	if hist[3].Role != message.RoleAssistant || hist[3].Content.Text() != "今天 28 度" {
		t.Errorf("final message = %+v", hist[3])
	}
	for _, m := range hist {
		if m.Role == message.RoleSystem {
			t.Fatalf("world-state section leaked into history: %+v", m)
		}
	}

	if sink.count() != 1 {
		t.Fatalf("memory sink turns = %d, want 1", sink.count())
	}
	turn := sink.turns[0]
	if len(turn.Messages) != 4 {
		t.Fatalf("sink messages = %d, want 4", len(turn.Messages))
	}
	if !strings.Contains(turn.Messages[1].Content.Text(), "tool_call: webfetch") {
		t.Errorf("sink assistant tool-call text = %q, want rendered tool_call line",
			turn.Messages[1].Content.Text())
	}
	if !strings.Contains(turn.Messages[2].Content.Text(), "tool_result: sunny 28C") {
		t.Errorf("sink tool text = %q, want rendered tool_result line",
			turn.Messages[2].Content.Text())
	}
}

// TestCommitHookExcludesCompactionSummary verifies that a compaction
// summary appended by the compact graph node as a marked user message
// is kept out of both the session archive and the memory raw window:
// it is derived context, not conversation.
func TestCommitHookExcludesCompactionSummary(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"memory":   sink,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatalf("factory New: %v", err)
	}
	committer := value.(agent.CommitterFunc)

	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: "s-1"}
	req := &agent.Request{
		ContextID: "s-1",
		Message:   message.NewTextMessage(message.RoleUser, "查一下天气"),
	}

	board := agent.NewBoard()
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "environment section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleSystem, "memory summary section"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "查一下天气"))
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "webfetch",
				Arguments: json.RawMessage(`{"url":"https://example.com"}`),
			}},
		}},
	})
	board.AppendChannelMessage(agent.MainChannel, message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: "sunny 28C",
			}},
		}},
	})
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "今天 28 度"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser,
			summarytext.SummaryPrefix+"\n查天气的进度与结论"))
	board.SetVar("world.sections.count", int64(2))

	res := &agent.Result{
		RunID:     "run-1",
		Status:    agent.StatusCompleted,
		LastBoard: board,
		Messages: []message.Message{
			message.NewTextMessage(message.RoleAssistant, "今天 28 度"),
		},
	}
	if err := committer(ctx, id, req, res); err != nil {
		t.Fatalf("commit: %v", err)
	}

	hist, err := store.History(ctx, "s-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 4 {
		t.Fatalf("history = %d messages, want 4 (user, tool-call assistant, tool result, final)", len(hist))
	}
	for _, m := range hist {
		if m.Role == message.RoleSystem {
			t.Fatalf("world-state section leaked into history: %+v", m)
		}
		if strings.HasPrefix(m.Content.Text(), summarytext.SummaryPrefix+"\n") {
			t.Fatalf("compaction summary leaked into history: %+v", m)
		}
	}
	if sink.count() != 1 {
		t.Fatalf("memory sink turns = %d, want 1", sink.count())
	}
	turn := sink.turns[0]
	if len(turn.Messages) != 4 {
		t.Fatalf("sink messages = %d, want 4", len(turn.Messages))
	}
	for _, m := range turn.Messages {
		if strings.HasPrefix(m.Content.Text(), summarytext.SummaryPrefix+"\n") {
			t.Fatalf("compaction summary leaked into memory raw window: %+v", m)
		}
	}
}

// TestCommitHookScopeFallbacks verifies the committer fills defaults when
// settings omit runtime/agent ids: runtime falls back to "opencraft" and the
// agent id falls back to the run's identity.
func TestCommitHookScopeFallbacks(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	value, err := (commitHookFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"memory":   sink,
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	committer := value.(agent.CommitterFunc)
	ctx := context.Background()
	id := agent.Identity{RunID: "run-1", AgentID: "fallback-agent", ConversationID: "s-1"}

	if err := committer(ctx, id, &agent.Request{ContextID: "s-1"}, &agent.Result{
		RunID:    "run-1",
		Messages: []message.Message{message.NewTextMessage(message.RoleAssistant, "hi")},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if sink.count() != 1 {
		t.Fatalf("memory sink turns = %d, want 1", sink.count())
	}
	scope := sink.turns[0].Scope
	if scope.RuntimeID != "opencraft" {
		t.Errorf("runtime id = %q, want opencraft default", scope.RuntimeID)
	}
	if scope.AgentID != "fallback-agent" {
		t.Errorf("agent id = %q, want identity fallback", scope.AgentID)
	}
}

// TestCommitHookFactoryMissingDeps verifies the factory fails closed when
// either required dependency is absent or mistyped.
func TestCommitHookFactoryMissingDeps(t *testing.T) {
	ctx := context.Background()
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	sink := &stubSink{}

	for name, deps := range map[string]map[string]any{
		"no memory":   {"sessions": store},
		"no sessions": {"memory": sink},
		"wrong type":  {"memory": store, "sessions": sink}, // both wrong types
	} {
		_, err := (commitHookFactory{}).New(ctx, resource.Input{
			Settings: []byte(`{}`),
			Deps:     deps,
		})
		if err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "want") {
			t.Errorf("%s: error = %v, want a validation error", name, err)
		}
	}
}

// TestCommitSettingsScopeFor unit-tests the scope derivation helper.
func TestCommitSettingsScopeFor(t *testing.T) {
	base := commitSettings{RuntimeID: "rt", UserID: "u", AgentID: "a"}
	if got := base.scopeFor(agent.Identity{AgentID: "other"}); got != (corememory.Scope{RuntimeID: "rt", UserID: "u", AgentID: "a"}) {
		t.Errorf("explicit settings scope = %+v", got)
	}

	// Runtime falls back to the opencraft default.
	noRuntime := commitSettings{UserID: "u", AgentID: "a"}
	if got := noRuntime.scopeFor(agent.Identity{AgentID: "other"}); got.RuntimeID != "opencraft" {
		t.Errorf("runtime fallback = %q, want opencraft", got.RuntimeID)
	}

	// Agent falls back to the identity.
	noAgent := commitSettings{RuntimeID: "rt", UserID: "u"}
	if got := noAgent.scopeFor(agent.Identity{AgentID: "identity-agent"}); got.AgentID != "identity-agent" {
		t.Errorf("agent fallback = %q, want identity-agent", got.AgentID)
	}

	// User id passes through unchanged and stays empty when unset.
	noUser := commitSettings{RuntimeID: "rt"}
	if got := noUser.scopeFor(agent.Identity{AgentID: "x"}); got.UserID != "" {
		t.Errorf("user id = %q, want empty", got.UserID)
	}
}
