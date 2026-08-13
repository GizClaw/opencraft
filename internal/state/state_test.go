package state

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

func TestOpenMigrateAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	thread := Thread{
		ID: "t1", AgentID: "agent-a", ContextID: "ctx-1",
		Title: "test", Metadata: map[string]any{"k": "v"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	item := Item{
		ID: "i1", ThreadID: "t1", TurnID: "turn-1", Seq: 1,
		ItemType: "text", Role: "user",
		Payload:   map[string]any{"text": "hello"},
		CreatedAt: now,
	}
	if err := s.AppendItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, err := s.LoadItems(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Payload["text"] != "hello" {
		t.Fatalf("items = %+v", items)
	}

	node := SummaryNode{
		ID: "s1", ThreadID: "t1", Level: 0,
		ParentIDs: nil, SourceIDs: []string{"i1"},
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "folded"},
		}},
		CreatedAt: now, UpdatedAt: now,
		Metadata: map[string]any{"algorithm": "summary_buffer"},
	}
	if err := s.UpsertSummaryNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.ListSummaryNodes(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || len(nodes[0].Content.Parts) != 1 {
		t.Fatalf("nodes = %+v", nodes)
	}

	// Idempotent migration on reopen.
	s2, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
}
