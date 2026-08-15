package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/memory/summary"
	"github.com/GizClaw/opencraft/internal/state"
)

// newSQLiteTurnStore opens a throwaway state DB and wraps it in the
// summary.TurnStore adapter used by the deploy assembly.
func newSQLiteTurnStore(t *testing.T) (*sqliteTurnStore, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &sqliteTurnStore{s: store}, store
}

// TestSQLiteTurnStoreAppendLoadRange covers the append / load-all / bounded
// range / count contract: seqs are contiguous from 0, ranges map back to
// original indices, and the count equals the text-message count.
func TestSQLiteTurnStoreAppendLoadRange(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, "m0"),
		message.NewTextMessage(message.RoleAssistant, "m1"),
		message.NewTextMessage(message.RoleUser, "m2"),
	}
	if err := adapter.AppendMessages(ctx, conv, "turn-1", msgs); err != nil {
		t.Fatal(err)
	}

	n, err := adapter.CountMessages(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}

	all, err := adapter.LoadMessages(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("load all = %d messages, want 3", len(all))
	}
	for i, m := range all {
		if want := "m" + string(rune('0'+i)); m.Content.Text() != want {
			t.Errorf("message %d = %q, want %q", i, m.Content.Text(), want)
		}
	}

	// Range [1,2] returns original indices 1..2 (inclusive).
	part, err := adapter.LoadMessagesRange(ctx, conv, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(part) != 2 || part[0].Content.Text() != "m1" || part[1].Content.Text() != "m2" {
		t.Errorf("range [1,2] = %+v", part)
	}

	// Empty range (from > to) returns nil, not an error.
	empty, err := adapter.LoadMessagesRange(ctx, conv, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if empty != nil {
		t.Errorf("empty range = %+v, want nil", empty)
	}
}

// TestSQLiteTurnStoreSkipsEmptyText verifies AppendMessages drops messages
// with empty text so the seq index space contains only text-bearing messages
// (the contract summary folding relies on for stable source IDs).
func TestSQLiteTurnStoreSkipsEmptyText(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, "kept"),
		{Role: message.RoleAssistant, Content: message.Content{}}, // empty text
		message.NewTextMessage(message.RoleUser, ""),              // empty text
		message.NewTextMessage(message.RoleAssistant, "kept-2"),
	}
	if err := adapter.AppendMessages(ctx, conv, "turn-1", msgs); err != nil {
		t.Fatal(err)
	}

	n, err := adapter.CountMessages(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2 (empty-text messages dropped)", n)
	}
	all, err := adapter.LoadMessages(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Content.Text() != "kept" || all[1].Content.Text() != "kept-2" {
		t.Errorf("messages = %+v", all)
	}
}

// TestSQLiteTurnStoreSeqAdvancesAcrossAppends verifies seqs continue across
// separate AppendMessages calls (the adapter reads MAX(seq) per append, not
// the item count), so interleaved appends cannot collide.
func TestSQLiteTurnStoreSeqAdvancesAcrossAppends(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	if err := adapter.AppendMessages(ctx, conv, "turn-1",
		[]message.Message{message.NewTextMessage(message.RoleUser, "a")}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AppendMessages(ctx, conv, "turn-2",
		[]message.Message{
			message.NewTextMessage(message.RoleAssistant, "b"),
			message.NewTextMessage(message.RoleUser, "c"),
		}); err != nil {
		t.Fatal(err)
	}

	all, err := adapter.LoadMessages(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("messages = %d, want 3", len(all))
	}
	for i, m := range all {
		if want := "abc"[i : i+1]; m.Content.Text() != want {
			t.Errorf("message %d = %q, want %q", i, m.Content.Text(), want)
		}
	}

	// Range across the append boundary still addresses original indices.
	part, err := adapter.LoadMessagesRange(ctx, conv, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(part) != 2 || part[0].Content.Text() != "b" || part[1].Content.Text() != "c" {
		t.Errorf("range [1,2] across appends = %+v", part)
	}
}

// TestSQLiteTurnStoreSummaryNodes covers the summary node roundtrip:
// upsert (insert + replace), list, and delete-by-level-keeping-one.
func TestSQLiteTurnStoreSummaryNodes(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	now := time.Now().UTC()
	n1 := summary.SummaryNode{
		ID:        "node-1",
		ThreadID:  conv,
		Level:     0,
		ParentIDs: nil,
		SourceIDs: []string{"s-1:turn-1:0", "s-1:turn-1:1"},
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "folded"}}},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]any{"policy": "raw=2"},
	}
	if err := adapter.UpsertSummaryNode(ctx, n1); err != nil {
		t.Fatal(err)
	}

	// Insert.
	nodes, err := adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].ID != "node-1" || nodes[0].Content.Text() != "folded" {
		t.Errorf("node = %+v", nodes[0])
	}
	if len(nodes[0].SourceIDs) != 2 || nodes[0].SourceIDs[1] != "s-1:turn-1:1" {
		t.Errorf("source ids = %v", nodes[0].SourceIDs)
	}

	// Replace (upsert same id).
	n1.Content = message.Content{Parts: []message.Part{message.TextPart{Text: "re-folded"}}}
	if err := adapter.UpsertSummaryNode(ctx, n1); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Content.Text() != "re-folded" {
		t.Fatalf("after upsert = %+v, want one re-folded node", nodes)
	}

	// Add a level-1 node and a level-0 sibling, then delete level 0
	// keeping node-1: only the level-0 sibling (node-3) is removed;
	// node-1 is kept and the level-1 node-2 is untouched.
	if err := adapter.UpsertSummaryNode(ctx, summary.SummaryNode{
		ID:        "node-2",
		ThreadID:  conv,
		Level:     1,
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "top"}}},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.UpsertSummaryNode(ctx, summary.SummaryNode{
		ID:        "node-3",
		ThreadID:  conv,
		Level:     0,
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "sibling"}}},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeleteSummaryNodes(ctx, conv, 0, "node-1"); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].ID != "node-1" || nodes[1].ID != "node-2" {
		t.Fatalf("after delete = %+v, want node-1 kept and node-2 untouched", nodes)
	}

	// An empty keepID removes every node at the level.
	if err := adapter.DeleteSummaryNodes(ctx, conv, 0, ""); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "node-2" {
		t.Fatalf("after delete-all level 0 = %+v, want only node-2", nodes)
	}
}

// TestSQLiteTurnStoreIsolation verifies conversations do not share items or
// summary nodes: seq index spaces and node lists are per-thread.
func TestSQLiteTurnStoreIsolation(t *testing.T) {
	adapter, _ := newSQLiteTurnStore(t)
	ctx := context.Background()

	if err := adapter.AppendMessages(ctx, "s-1", "turn-1",
		[]message.Message{message.NewTextMessage(message.RoleUser, "conv-a")}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AppendMessages(ctx, "s-2", "turn-1",
		[]message.Message{message.NewTextMessage(message.RoleUser, "conv-b")}); err != nil {
		t.Fatal(err)
	}

	// Each conversation keeps its own message set.
	want := map[string]string{"s-1": "conv-a", "s-2": "conv-b"}
	for conv := range want {
		all, err := adapter.LoadMessages(ctx, conv)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 || all[0].Content.Text() != want[conv] {
			t.Errorf("conv %s: %+v, want one message %q", conv, all, want[conv])
		}
	}
}
