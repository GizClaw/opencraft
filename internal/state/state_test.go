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

func TestUpsertSummaryNodeReplacesAndDeleteSummaryNodes(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if err := s.CreateThread(ctx, Thread{
		ID: "t1", AgentID: "agent-a", ContextID: "ctx-1",
		Title: "test", Metadata: map[string]any{},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	node := SummaryNode{
		ID: "s1", ThreadID: "t1", Level: 0,
		SourceIDs: []string{"i1"},
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "v1"},
		}},
		CreatedAt: now, UpdatedAt: now,
		Metadata: map[string]any{"algorithm": "summary_buffer"},
	}
	if err := s.UpsertSummaryNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	// Re-upserting the same ID with new content and source IDs must replace
	// every mutable column: the rolling summary updates SourceIDs in place.
	updated := node
	updated.SourceIDs = []string{"i1", "i2"}
	updated.Content = message.Content{Parts: []message.Part{
		message.TextPart{Text: "v2"},
	}}
	if err := s.UpsertSummaryNode(ctx, updated); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.ListSummaryNodes(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].Content.Text() != "v2" {
		t.Fatalf("content = %q, want v2", nodes[0].Content.Text())
	}
	if len(nodes[0].SourceIDs) != 2 || nodes[0].SourceIDs[0] != "i1" || nodes[0].SourceIDs[1] != "i2" {
		t.Fatalf("source ids = %v, want [i1 i2] replaced on conflict", nodes[0].SourceIDs)
	}

	// DeleteSummaryNodes keeps only the node whose id equals keepID.
	if err := s.DeleteSummaryNodes(ctx, "t1", 0, "s1"); err != nil {
		t.Fatal(err)
	}
	nodes, err = s.ListSummaryNodes(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d after delete-keeping-s1, want 1", len(nodes))
	}

	if err := s.DeleteSummaryNodes(ctx, "t1", 0, "other"); err != nil {
		t.Fatal(err)
	}
	nodes, err = s.ListSummaryNodes(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("nodes = %d after delete-keeping-other, want 0", len(nodes))
	}
}
