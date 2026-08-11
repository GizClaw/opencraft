package summary

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/sdk/memory"
	"github.com/GizClaw/flowcraft/sdk/message"
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
