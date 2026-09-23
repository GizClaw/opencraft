package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// commitHook wires the production commit hook over a migrated session
// store, exactly the way the deploy assembly does: the memory resource is
// the summary assembly, so the hook writes the turn to the transcript and
// the assembly folds what the transcript holds.
func commitHook(t *testing.T) (agent.CommitterFunc, *sessions.Store, *sqliteTurnStore) {
	t.Helper()
	store, err := newMigratedSessions(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	adapter := &sqliteTurnStore{db: store.Database()}
	res := summary.NewAssembly(adapter)
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

// TestProjectionServesTheModelWindow drives the commit path and reads the
// window the model would get: raw items carry transcript seqs, and a tool
// pair arrives intact with its activity rendered on read.
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
	raws := rawItems(got)
	// The window starts at the user request and includes the whole tool
	// round: call, result, reply.
	want := []struct {
		role message.Role
		text string
	}{
		{message.RoleUser, "run the tool"},
		{message.RoleAssistant, ""},
		{message.RoleTool, ""},
		{message.RoleAssistant, "done"},
	}
	if len(raws) != len(want) {
		t.Fatalf("raw items = %+v, want %d", raws, len(want))
	}
	for i, w := range want {
		id := summary.SourceID(int64(i))
		if raws[i].ID != id || raws[i].MessageRole != w.role {
			t.Errorf("raw item %d = %s %s, want %s seq %s",
				i, raws[i].MessageRole, raws[i].ID, w.role, id)
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

// TestProjectionWindowIncludesAppAuthoredTurns pins rule 1 and rule 2:
// every archived row is a candidate whoever wrote it, so an app-authored
// delegation note reaches the model on the next turn, while an imported
// conversation's own system prompt stays in the archive. The note arrives
// as the newest row and must not disturb the tool pair before it.
func TestProjectionWindowIncludesAppAuthoredTurns(t *testing.T) {
	committer, store, adapter := commitHook(t)
	ctx := context.Background()
	const conv = "s-notes"

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
	const noteText = "[delegated worker scout finished: completed]\nfound the bug"
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

	assembly := summary.NewAssembly(adapter, summary.WithAssemblyPolicy(summary.Policy{
		MaxRawMessages: 50, PreserveRecent: 0, MaxSummaryBytes: 4096,
	}))
	got, err := assembly.Context(ctx, corememory.ContextRequest{
		Scope:          corememory.Scope{RuntimeID: "opencraft"},
		ConversationID: conv,
		Budget:         corememory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raws := rawItems(got)
	// request, call, result, reply, note — and no system row.
	if len(raws) != 5 {
		t.Fatalf("raw items = %d, want 5 (%+v)", len(raws), raws)
	}
	note := raws[4]
	if note.MessageRole != message.RoleUser || note.Content.Text() != noteText {
		t.Errorf("note row = %s %q, want the user-role note text",
			note.MessageRole, note.Content.Text())
	}
	if note.ID != summary.SourceID(4) {
		t.Errorf("note id = %q, want its transcript coordinate", note.ID)
	}
	for _, item := range raws {
		if strings.Contains(item.Content.Text(), systemText) {
			t.Fatalf("imported system prompt reached the window: %+v", item)
		}
	}
	// The tool pair ahead of the note is still a pair.
	if raws[1].MessageRole != message.RoleAssistant ||
		raws[2].MessageRole != message.RoleTool {
		t.Errorf("tool pair = %s / %s, want assistant call then tool result",
			raws[1].MessageRole, raws[2].MessageRole)
	}
}

// TestProjectionServesTextlessRowAsPlaceholder pins rule 3 end to end: an
// attachment-only turn reaches the model as a placeholder naming what it
// carries, at its own transcript coordinate, instead of vanishing from the
// conversation.
func TestProjectionServesTextlessRowAsPlaceholder(t *testing.T) {
	committer, _, adapter := commitHook(t)
	ctx := context.Background()
	const conv = "s-image"

	image, err := media.NewImageURL("https://example.invalid/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id := agent.Identity{RunID: "run-1", AgentID: "assistant", ConversationID: conv}
	req := &agent.Request{
		ContextID: conv,
		Message:   message.NewTextMessage(message.RoleUser, "look at this"),
	}
	res := &agent.Result{
		RunID:  "run-1",
		Status: agent.StatusCompleted,
		Messages: []message.Message{
			{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
				message.ImagePart{Source: image},
			}}},
		},
	}
	if err := committer(ctx, id, req, res); err != nil {
		t.Fatal(err)
	}

	assembly := summary.NewAssembly(adapter, summary.WithAssemblyPolicy(summary.Policy{
		MaxRawMessages: 10, PreserveRecent: 0, MaxSummaryBytes: 4096,
	}))
	got, err := assembly.Context(ctx, corememory.ContextRequest{
		Scope:          corememory.Scope{RuntimeID: "opencraft"},
		ConversationID: conv,
		Budget:         corememory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raws := rawItems(got)
	if len(raws) != 2 {
		t.Fatalf("raw items = %d, want the request and its placeholder", len(raws))
	}
	if got := raws[1].Content.Text(); got != "[attachment: image]" {
		t.Errorf("image row = %q, want the attachment placeholder", got)
	}
	if raws[1].MessageRole != message.RoleAssistant ||
		raws[1].ID != summary.SourceID(1) {
		t.Errorf("image row = %s %s, want assistant at seq 1",
			raws[1].MessageRole, raws[1].ID)
	}
}

// rawItems filters a context result down to its raw window rows.
func rawItems(res corememory.ContextResult) []corememory.ContextItem {
	var raws []corememory.ContextItem
	for _, item := range res.Items {
		if item.Kind == corememory.ContextRawMessage {
			raws = append(raws, item)
		}
	}
	return raws
}
