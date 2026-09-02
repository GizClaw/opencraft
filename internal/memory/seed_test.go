package memory

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/memory/summary"
)

func TestSeedConversationRendersToolActivityAndSkipsSystem(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	assembly := summary.NewAssembly(adapter)
	ctx := context.Background()

	msgs := []message.Message{
		message.NewTextMessage(message.RoleSystem, "source app system prompt"),
		message.NewTextMessage(message.RoleUser, "fix the build"),
		{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID:  "call-1",
					Content: "build output",
				}},
			}},
		},
		message.NewTextMessage(message.RoleAssistant, "done"),
	}
	if err := SeedConversation(
		ctx, assembly, "s-1", "codex:conv-1", msgs,
	); err != nil {
		t.Fatalf("SeedConversation: %v", err)
	}

	loaded, err := adapter.LoadMessages(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("seeded messages = %d, want 3 (system skipped)", len(loaded))
	}
	if loaded[0].Content.Text() != "fix the build" {
		t.Errorf("first message = %q", loaded[0].Content.Text())
	}
	if loaded[1].Role != message.RoleTool ||
		loaded[1].Content.Text() != "tool_result: build output" {
		t.Errorf("tool message = %+v", loaded[1])
	}
	if loaded[2].Content.Text() != "done" {
		t.Errorf("last message = %q", loaded[2].Content.Text())
	}
}

func TestSeedConversationValidatesInputs(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	assembly := summary.NewAssembly(adapter)
	ctx := context.Background()

	if err := SeedConversation(ctx, nil, "s-1", "src", nil); err == nil {
		t.Fatal("nil assembly accepted")
	}
	if err := SeedConversation(
		ctx, assembly, "s-1", "", nil,
	); err == nil {
		t.Fatal("empty source id accepted")
	}
	if err := SeedConversation(
		ctx, assembly, "", "src",
		[]message.Message{message.NewTextMessage(message.RoleUser, "hi")},
	); err == nil {
		t.Fatal("empty conversation id accepted")
	}
	if err := SeedConversation(
		ctx, assembly, "s-1", "src",
		[]message.Message{{Role: message.RoleSystem, Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "prompt"}},
		}}},
	); err == nil {
		t.Fatal("seed with only system messages accepted")
	}
}
