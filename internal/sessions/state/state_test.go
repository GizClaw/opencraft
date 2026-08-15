package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

func TestOpenMigrateAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

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

// TestStateServesCheckpoints verifies that the opencraft state store
// is the single SQLite owner: it implements agent.CheckpointStore on
// its own connection instead of a second backend sharing the file.
func TestStateServesCheckpoints(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions", "session.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// The checkpoint schema is part of state's own migrations.
	rows, err := s.db.Query(
		"SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"items", "summary_nodes", "schema_migrations", "agent_checkpoints",
	} {
		if !tables[want] {
			t.Errorf("shared session.db missing table %q (tables: %v)",
				want, tables)
		}
	}

	// state.Store persists and loads checkpoints on its own handle.
	cp := agent.Checkpoint{
		ExecID:    "run-1",
		Steps:     []string{"step-a"},
		Iteration: 2,
		Board: &agent.BoardSnapshot{
			Vars: map[string]any{"k": "v"},
		},
		Attributes: map[string]string{"graph": "assistant"},
		Timestamp:  time.Now().UTC(),
	}
	if err := s.Save(ctx, cp); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	loaded, err := s.Load(ctx, "run-1")
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if loaded == nil || loaded.ExecID != "run-1" ||
		loaded.Iteration != 2 || len(loaded.Steps) != 1 ||
		loaded.Board == nil {
		t.Fatalf("loaded checkpoint = %+v", loaded)
	}
	// A missing exec id returns (nil, nil).
	missing, err := s.Load(ctx, "nope")
	if err != nil || missing != nil {
		t.Fatalf("load missing = %v, %v", missing, err)
	}
	// List and Delete round out the optional interfaces.
	ids, err := s.List(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "run-1" {
		t.Fatalf("list = %v, %v", ids, err)
	}
	if err := s.Delete(ctx, "run-1"); err != nil {
		t.Fatalf("delete checkpoint: %v", err)
	}
	if got, _ := s.Load(ctx, "run-1"); got != nil {
		t.Fatal("checkpoint not deleted")
	}
}

func TestUpsertSummaryNodeReplacesAndDeleteSummaryNodes(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

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

func TestCountNextSeqAndLoadItemsRange(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	now := time.Now().UTC()
	if err := s.CreateThread(ctx, Thread{
		ID: "t1", AgentID: "agent-a", ContextID: "ctx-1",
		Title: "test", Metadata: map[string]any{},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// NextSeq starts at 0 for an empty thread.
	seq, err := s.NextSeq(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("next seq = %d, want 0 for empty thread", seq)
	}

	for i := int64(0); i < 5; i++ {
		if err := s.AppendItem(ctx, Item{
			ID: "i" + itoaTest(i), ThreadID: "t1", TurnID: "turn",
			Seq: i, ItemType: "text", Role: "user",
			Payload:   map[string]any{"text": "msg"},
			CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.CountItems(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("count = %d, want 5", n)
	}
	seq, err = s.NextSeq(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 5 {
		t.Fatalf("next seq = %d, want 5", seq)
	}

	items, err := s.LoadItemsRange(ctx, "t1", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Seq != 1 || items[2].Seq != 3 {
		t.Fatalf("range items = %+v, want seqs 1..3", items)
	}

	// Empty range and out-of-range bounds are safe.
	items, err = s.LoadItemsRange(ctx, "t1", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("empty range returned %d items, want 0", len(items))
	}
	items, err = s.LoadItemsRange(ctx, "t1", 0, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("clamped range returned %d items, want 5", len(items))
	}
}

func itoaTest(i int64) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
