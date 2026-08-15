package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/sessions"
)

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
	if len(turn.Messages) != 1 || turn.Messages[0].Content.Text() != "好的，已实现" {
		t.Errorf("sink messages = %+v", turn.Messages)
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
