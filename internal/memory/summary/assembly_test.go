package summary

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

type fakeTurnStore struct {
	msgs  map[string][]message.Message
	nodes []SummaryNode
}

func (f *fakeTurnStore) AppendMessages(_ context.Context, conversationID, _ string, msgs []message.Message) error {
	f.msgs[conversationID] = append(f.msgs[conversationID], msgs...)
	return nil
}

func (f *fakeTurnStore) LoadMessages(_ context.Context, conversationID string) ([]message.Message, error) {
	return f.msgs[conversationID], nil
}

func (f *fakeTurnStore) UpsertSummaryNode(_ context.Context, n SummaryNode) error {
	for i := range f.nodes {
		if f.nodes[i].ID == n.ID {
			f.nodes[i] = n
			return nil
		}
	}
	f.nodes = append(f.nodes, n)
	return nil
}

func (f *fakeTurnStore) ListSummaryNodes(_ context.Context, conversationID string) ([]SummaryNode, error) {
	var out []SummaryNode
	for _, n := range f.nodes {
		if n.ThreadID == conversationID {
			out = append(out, n)
		}
	}
	return out, nil
}

func TestAssemblyCommitTurnFoldsOverWindow(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{MaxRawMessages: 2, PreserveRecent: 2}))

	for i := 0; i < 2; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: "t" + string(rune('0'+i)),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, "msg"),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.nodes) != 0 {
		t.Fatalf("want no fold at window boundary, got %d nodes", len(store.nodes))
	}

	turn := memory.Turn{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		IdempotencyKey: "t3",
		Messages: []message.Message{
			message.NewTextMessage(message.RoleUser, "third"),
		},
	}
	if err := a.CommitTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("want 1 fold, got %d", len(store.nodes))
	}
	if len(store.nodes[0].SourceIDs) != 1 {
		t.Fatalf("source ids = %v, want first message only", store.nodes[0].SourceIDs)
	}

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 1, MaxChars: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Kind != memory.ContextSummary {
		t.Fatalf("context items = %+v", res.Items)
	}
}

func TestAssemblyRejectsDocuments(t *testing.T) {
	a := NewAssembly(&fakeTurnStore{msgs: map[string][]message.Message{}})
	err := a.PutDocument(context.Background(), memory.Document{})
	if err == nil {
		t.Fatal("want error for document sink")
	}
}

func TestAssemblyContextReturnsRecentRawMessages(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{
		"c1": {
			message.NewTextMessage(message.RoleUser, "first"),
			message.NewTextMessage(message.RoleAssistant, "answer one"),
		},
	}}
	a := NewAssembly(store)

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 8, MaxChars: 1 << 16},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want 2 raw messages", len(res.Items))
	}
	if res.Items[0].Kind != memory.ContextRawMessage ||
		res.Items[0].Content.Text() != "first" ||
		res.Items[1].Content.Text() != "answer one" {
		t.Fatalf("items = %+v, want chronological raw messages", res.Items)
	}
	if res.Truncated {
		t.Fatal("Truncated = true, want false when everything fits")
	}
}

func TestAssemblyContextSkipsFoldedRawMessages(t *testing.T) {
	ctx := context.Background()
	first := message.NewTextMessage(message.RoleUser, "first")
	store := &fakeTurnStore{
		msgs: map[string][]message.Message{
			"c1": {first, message.NewTextMessage(message.RoleAssistant, "answer one")},
		},
		nodes: []SummaryNode{{
			ID:        "node-1",
			ThreadID:  "c1",
			Level:     0,
			SourceIDs: []string{stableMessageID("c1", 0, first)},
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "summary of first"},
			}},
		}},
	}
	a := NewAssembly(store)

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 8, MaxChars: 1 << 16},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want summary + uncovered raw", len(res.Items))
	}
	if res.Items[0].Kind != memory.ContextSummary ||
		res.Items[1].Kind != memory.ContextRawMessage ||
		res.Items[1].Content.Text() != "answer one" {
		t.Fatalf("items = %+v, want summary then uncovered raw", res.Items)
	}
}

func TestAssemblyContextBudgetKeepsRecentTail(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{
		"c1": {
			message.NewTextMessage(message.RoleUser, "first"),
			message.NewTextMessage(message.RoleUser, "second"),
			message.NewTextMessage(message.RoleUser, "third"),
		},
	}}
	a := NewAssembly(store)

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 0, MaxChars: len("third")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Content.Text() != "third" {
		t.Fatalf("items = %+v, want only the newest raw message", res.Items)
	}
	if !res.Truncated {
		t.Fatal("Truncated = false, want true when raw messages were dropped")
	}
}
