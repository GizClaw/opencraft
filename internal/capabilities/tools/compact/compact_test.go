package compact

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

func patchSummary(t *testing.T, out string) string {
	t.Helper()
	var p Patch
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("decode patch: %v\n%s", err, out)
	}
	text := p.Message.Content.Text()
	if len(text) <= len(summarytext.SummaryPrefix)+1 ||
		text[:len(summarytext.SummaryPrefix)+1] != summarytext.SummaryPrefix+"\n" {
		t.Fatalf("patch message is not marked: %q", text)
	}
	return text[len(summarytext.SummaryPrefix)+1:]
}

// convMsg renders one wire message with a single text part, JSON-escaped.
func convMsg(role, text string) string {
	esc := strings.ReplaceAll(text, "\\", "\\\\")
	esc = strings.ReplaceAll(esc, "\"", "\\\"")
	esc = strings.ReplaceAll(esc, "\n", "\\n")
	return `{"role":"` + role + `","content":{"parts":[{"type":"text","text":"` + esc + `"}]}}`
}

func TestExecuteCondensesAndPersistsArtifact(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	tool := &Tool{
		store: store,
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			calls++
			if len(req.Context) != 1 ||
				req.Context[0].Role != message.RoleSystem ||
				!strings.Contains(req.Context[0].Content.Text(),
					"CONTEXT CHECKPOINT COMPACTION") {
				t.Errorf("condense request missing system instruction: %+v",
					req.Context)
			}
			if !strings.Contains(req.Input.Content.Text(), "m1") {
				t.Errorf("condense input missing conversation: %q",
					req.Input.Content.Text())
			}
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S1"),
			}, nil
		},
	}
	ctx := context.Background()
	args := `{"conversation":[` +
		convMsg("user", "m1") + `,` +
		convMsg("assistant", "m2") + `],` +
		`"budget_chars":100,"conversation_id":"s-1"}`

	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := patchSummary(t, out); got != "S1" {
		t.Fatalf("summary = %q, want S1", got)
	}
	if calls != 1 {
		t.Fatalf("generate calls = %d, want 1", calls)
	}

	// Re-compacting the same set must reuse the persisted artifact
	// instead of condensing again.
	out2, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("execute again: %v", err)
	}
	if got := patchSummary(t, out2); got != "S1" || calls != 1 {
		t.Fatalf("reuse = %q calls=%d, want S1 calls=1", got, calls)
	}
}

func TestExecuteMergesNewMessagesWithArtifact(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	tool := &Tool{
		store: store,
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			text := req.Input.Content.Text()
			calls++
			if calls == 1 {
				return inference.GenerateResponse{
					Message: message.NewTextMessage(message.RoleAssistant, "S1"),
				}, nil
			}
			if !strings.Contains(text, "S1") {
				t.Errorf("condense input must merge previous summary: %q", text)
			}
			if !strings.Contains(text, "m3") {
				t.Errorf("condense input must contain new message: %q", text)
			}
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S2"),
			}, nil
		},
	}
	ctx := context.Background()
	first := `{"conversation":[` + convMsg("user", "m1") + `,` + convMsg("assistant", "m2") + `],` +
		`"budget_chars":100,"conversation_id":"s-1"}`
	if _, err := tool.Execute(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := `{"conversation":[` +
		convMsg("user", "m1") + `,` + convMsg("assistant", "m2") + `,` +
		convMsg("tool", "m3") + `],` +
		`"budget_chars":100,"conversation_id":"s-1"}`
	out, err := tool.Execute(ctx, second)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := patchSummary(t, out); got != "S2" {
		t.Fatalf("summary = %q, want S2", got)
	}

	// The artifact now covers all three messages.
	var art artifact
	if err := store.ReadState("s-1", compactStateName, &art); err != nil {
		t.Fatal(err)
	}
	if len(art.Covered) != 3 {
		t.Fatalf("covered = %v, want 3 ids", art.Covered)
	}
	if art.Summary != "S2" {
		t.Fatalf("artifact summary = %q, want S2", art.Summary)
	}
}

// TestExecuteSkipsSummaryMarkedMessages verifies that messages carrying
// the compaction summary marker are not fed back into the condensation:
// re-running with the same messages plus the injected summary returns
// the persisted artifact without another LLM call, and a genuine new
// message is condensed together with the stored summary.
func TestExecuteSkipsSummaryMarkedMessages(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	tool := &Tool{
		store: store,
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			text := req.Input.Content.Text()
			calls++
			if strings.Contains(text, summarytext.SummaryPrefix) {
				t.Errorf("condense input must skip marked summary message: %q", text)
			}
			if calls == 1 {
				return inference.GenerateResponse{
					Message: message.NewTextMessage(message.RoleAssistant, "S1"),
				}, nil
			}
			if !strings.Contains(text, "S1") {
				t.Errorf("condense input must merge previous summary: %q", text)
			}
			if !strings.Contains(text, "m3") {
				t.Errorf("condense input must contain new message: %q", text)
			}
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S2"),
			}, nil
		},
	}
	ctx := context.Background()
	marked := summarytext.SummaryPrefix + "\nS1"
	first := `{"conversation":[` + convMsg("user", "m1") + `,` + convMsg("assistant", "m2") + `],` +
		`"budget_chars":100,"conversation_id":"s-1"}`
	if _, err := tool.Execute(ctx, first); err != nil {
		t.Fatal(err)
	}

	// Same fold plus the injected summary: nothing new to condense, so
	// the persisted summary is reused without another LLM call.
	reuse := `{"conversation":[` +
		convMsg("user", "m1") + `,` + convMsg("assistant", "m2") + `,` +
		convMsg("user", marked) + `],` +
		`"budget_chars":100,"conversation_id":"s-1"}`
	out, err := tool.Execute(ctx, reuse)
	if err != nil {
		t.Fatalf("execute reuse: %v", err)
	}
	if got := patchSummary(t, out); got != "S1" || calls != 1 {
		t.Fatalf("reuse = %q calls=%d, want S1 calls=1", got, calls)
	}

	// A genuine new message alongside the marked summary: condensed
	// once, with the stored summary merged but the marked message not
	// repeated in the input.
	next := `{"conversation":[` +
		convMsg("user", "m1") + `,` + convMsg("assistant", "m2") + `,` +
		convMsg("user", marked) + `,` +
		convMsg("tool", "m3") + `],` +
		`"budget_chars":100,"conversation_id":"s-1"}`
	out, err = tool.Execute(ctx, next)
	if err != nil {
		t.Fatalf("execute next: %v", err)
	}
	if got := patchSummary(t, out); got != "S2" || calls != 2 {
		t.Fatalf("next = %q calls=%d, want S2 calls=2", got, calls)
	}
}

// TestExecuteRendersToolActivity verifies that tool calls and results
// carried in the folded messages reach the condensation prompt: they
// are rendered as tool_call / tool_result text lines instead of being
// silently dropped.
func TestExecuteRendersToolActivity(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	tool := &Tool{
		store: store,
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			got = req.Input.Content.Text()
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S1"),
			}, nil
		},
	}
	args := map[string]any{
		"conversation": []message.Message{
			{
				Role: message.RoleAssistant,
				Content: message.Content{Parts: []message.Part{
					message.ToolCallPart{Call: message.ToolCall{
						ID: "c1", Name: "exec_command",
						Arguments: json.RawMessage(`{"cmd":"go test ./..."}`),
					}},
				}},
			},
			{
				Role: message.RoleTool,
				Content: message.Content{Parts: []message.Part{
					message.ToolResultPart{Result: message.ToolResult{
						CallID: "c1", Content: "build ok",
					}},
				}},
			},
		},
		"budget_chars":    100,
		"conversation_id": "s-1",
	}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), string(data)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(got, `tool_call: exec_command {"cmd":"go test ./..."}`) {
		t.Errorf("condense input missing tool call rendering: %q", got)
	}
	if !strings.Contains(got, "tool_result: build ok") {
		t.Errorf("condense input missing tool result rendering: %q", got)
	}
}

// TestRenderSystemPrompt verifies the embedded template renders the
// handoff instruction as the system message.
func TestRenderSystemPrompt(t *testing.T) {
	got, err := renderSystemPrompt()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "CONTEXT CHECKPOINT COMPACTION") {
		t.Errorf("prompt missing instruction: %q", got)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("prompt must not carry template placeholders: %q", got)
	}
}

func TestExecuteRejectsEmptyConversation(t *testing.T) {
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{store: store}
	if _, err := tool.Execute(context.Background(), `{"conversation":[]}`); err == nil {
		t.Fatal("empty conversation must fail")
	}
}
