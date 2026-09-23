package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// commitHook wires the production commit hook over a migrated session
// store, exactly the way the deploy assembly does.
func commitHook(t *testing.T) (agent.CommitterFunc, *sessions.Store, *sqliteTurnStore) {
	t.Helper()
	store, err := newMigratedSessions(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
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
	committer, ok := value.(agent.CommitterFunc)
	if !ok {
		t.Fatalf("value = %T, want agent.CommitterFunc", value)
	}
	return committer, store, adapter
}

// TestProjectionMatchesStoredMemoryRows is increment A's safety rope: the
// window is now projected from the transcript, and the memory copy the
// previous read path served is still written in the same transaction.
// Both must answer identically, row for row, or switching the read path
// changed what the model sees. The test goes away with memory_items.
func TestProjectionMatchesStoredMemoryRows(t *testing.T) {
	committer, store, adapter := commitHook(t)
	ctx := context.Background()
	const conv = "s-parity"

	turn := func(runID, request string, tail ...message.Message) {
		t.Helper()
		id := agent.Identity{
			RunID: runID, AgentID: "assistant", ConversationID: conv,
		}
		req := &agent.Request{
			ContextID: conv,
			Message:   message.NewTextMessage(message.RoleUser, request),
		}
		res := &agent.Result{
			RunID:    runID,
			Status:   agent.StatusCompleted,
			Messages: tail,
		}
		if err := committer(ctx, id, req, res); err != nil {
			t.Fatalf("commit %s: %v", runID, err)
		}
	}

	turn("run-1", "first question",
		message.NewTextMessage(message.RoleAssistant, "first answer"))
	// A tool round inside one turn: the pair must survive the projection
	// with the rendered activity the stored copy carries.
	turn("run-2", "run the tool",
		message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "fetch",
					Arguments: json.RawMessage(`{"url":"https://example.invalid"}`),
				}},
			}},
		},
		message.Message{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1", Content: message.NewTextContent("tool output"),
				}},
			}},
		},
		message.NewTextMessage(message.RoleAssistant, "tool done"),
	)
	turn("run-3", "and again",
		message.NewTextMessage(message.RoleAssistant, "third answer"))

	// Rows the write path never put in the window: an app-authored turn
	// (the delegation note the host appends on its own) and an imported
	// conversation's system prompt. The transcript keeps both; the
	// projection must leave both out.
	const noteText = "[delegated worker scout finished: completed]"
	if err := store.AppendTurnWithOriginAndRunID(ctx, conv, "run-note",
		sessions.TurnOrigin{Kind: "delegation_note"},
		[]message.Message{message.NewTextMessage(message.RoleUser, noteText)},
	); err != nil {
		t.Fatal(err)
	}
	const systemText = "source app system prompt"
	if err := store.AppendTurnWithRunID(ctx, conv, "run-system",
		[]message.Message{message.NewTextMessage(message.RoleSystem, systemText)},
	); err != nil {
		t.Fatal(err)
	}

	stored := loadStoredMemoryRows(t, store.Database(), conv)
	projected, err := adapter.LoadAll(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	// Three turns of user + reply, with turn 2 carrying a tool round. The
	// note turn and the system row are not part of it; an empty comparison
	// must fail rather than pass vacuously.
	if len(stored) != 8 {
		t.Fatalf("stored memory rows = %d, want 8", len(stored))
	}
	if len(projected) != len(stored) {
		t.Fatalf("projected rows = %d, stored memory rows = %d, want equal",
			len(projected), len(stored))
	}
	for i := range stored {
		got, err := json.Marshal(projected[i].Message().Content)
		if err != nil {
			t.Fatalf("marshal projected row %d: %v", i, err)
		}
		want, err := json.Marshal(stored[i].Content)
		if err != nil {
			t.Fatalf("marshal stored row %d: %v", i, err)
		}
		if projected[i].Role != stored[i].Role || !bytes.Equal(got, want) {
			t.Errorf("row %d:\n project %s %s\n  stored %s %s",
				i, projected[i].Role, got, stored[i].Role, want)
		}
	}
	for _, row := range projected {
		text := row.Content.Text()
		if text == noteText || text == systemText {
			t.Fatalf("row %d leaked a transcript-only row into the window: %q",
				row.Seq, text)
		}
	}
}

// TestProjectionServesTheModelWindow drives the same commit path and reads
// the window the model would get, so the rope above is tied to the real
// read: raw items carry transcript seqs, and a tool pair arrives intact.
func TestProjectionServesTheModelWindow(t *testing.T) {
	committer, _, adapter := commitHook(t)
	ctx := context.Background()
	const conv = "s-window"

	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: conv}
	req := &agent.Request{
		ContextID: conv,
		Message:   message.NewTextMessage(message.RoleUser, "run the tool"),
	}
	res := &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusCompleted,
		Messages: []message.Message{
			{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "fetch", Arguments: json.RawMessage(`{}`),
				}},
			}}},
			{Role: message.RoleTool, Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1", Content: message.NewTextContent("tool output"),
				}},
			}}},
			message.NewTextMessage(message.RoleAssistant, "done"),
		},
	}
	if err := committer(ctx, id, req, res); err != nil {
		t.Fatal(err)
	}

	assembly := summary.NewAssembly(adapter, summary.WithAssemblyPolicy(summary.Policy{
		MaxRawMessages: 2, PreserveRecent: 0, MaxSummaryBytes: 4096,
	}))
	got, err := assembly.Context(ctx, corememory.ContextRequest{
		Scope:          corememory.Scope{RuntimeID: "opencraft"},
		ConversationID: conv,
		Budget:         corememory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raws []corememory.ContextItem
	for _, item := range got.Items {
		if item.Kind == corememory.ContextRawMessage {
			raws = append(raws, item)
		}
	}
	// The window starts at the user request and includes the whole tool
	// round: call, result, reply.
	want := []struct {
		id   string
		role message.Role
		text string
	}{
		{summary.SourceID(0), message.RoleUser, "run the tool"},
		{summary.SourceID(1), message.RoleAssistant, ""},
		{summary.SourceID(2), message.RoleTool, ""},
		{summary.SourceID(3), message.RoleAssistant, "done"},
	}
	if len(raws) != len(want) {
		t.Fatalf("raw items = %+v, want %d", raws, len(want))
	}
	for i, w := range want {
		if raws[i].ID != w.id || raws[i].MessageRole != w.role {
			t.Errorf("raw item %d = %s %s, want %s %s",
				i, raws[i].MessageRole, raws[i].ID, w.role, w.id)
		}
		if w.text != "" && raws[i].Content.Text() != w.text {
			t.Errorf("raw item %d text = %q, want %q",
				i, raws[i].Content.Text(), w.text)
		}
	}
	// Tool activity is readable in the window (the rendered text part),
	// while the call id stays structured in the parts.
	if !strings.Contains(raws[1].Content.Text(), "tool_call: fetch") {
		t.Errorf("call row text = %q, want rendered tool activity",
			raws[1].Content.Text())
	}
	if calls := raws[1].Content.Parts; len(calls) == 0 {
		t.Error("call row lost its structured part")
	}
}
