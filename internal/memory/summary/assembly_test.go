package summary

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
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

// recordingStore wraps fakeTurnStore and counts which load path the
// assembly takes, so tests can assert the incremental fold and context
// never fall back to a full conversation scan.
type recordingStore struct {
	*fakeTurnStore
	fullLoads  int
	rangeLoads int
	countCalls int
	ranges     [][2]int
}

func (r *recordingStore) LoadMessages(ctx context.Context, conversationID string) ([]message.Message, error) {
	r.fullLoads++
	return r.fakeTurnStore.LoadMessages(ctx, conversationID)
}

func (r *recordingStore) CountMessages(ctx context.Context, conversationID string) (int, error) {
	r.countCalls++
	return r.fakeTurnStore.CountMessages(ctx, conversationID)
}

func (r *recordingStore) LoadMessagesRange(ctx context.Context, conversationID string, from, to int) ([]message.Message, error) {
	r.rangeLoads++
	r.ranges = append(r.ranges, [2]int{from, to})
	return r.fakeTurnStore.LoadMessagesRange(ctx, conversationID, from, to)
}

func (f *fakeTurnStore) LoadMessages(_ context.Context, conversationID string) ([]message.Message, error) {
	return f.msgs[conversationID], nil
}

func (f *fakeTurnStore) CountMessages(_ context.Context, conversationID string) (int, error) {
	return len(f.msgs[conversationID]), nil
}

func (f *fakeTurnStore) LoadMessagesRange(_ context.Context, conversationID string, from, to int) ([]message.Message, error) {
	msgs := f.msgs[conversationID]
	if from < 0 {
		from = 0
	}
	if to >= len(msgs) {
		to = len(msgs) - 1
	}
	if from > to {
		return nil, nil
	}
	return msgs[from : to+1], nil
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

func (f *fakeTurnStore) DeleteSummaryNodes(_ context.Context, conversationID string, level int, keepID string) error {
	var out []SummaryNode
	for _, n := range f.nodes {
		if n.ThreadID == conversationID && n.Level == level && n.ID != keepID {
			continue
		}
		out = append(out, n)
	}
	f.nodes = out
	return nil
}

func TestAssemblyCommitTurnFoldsOverWindow(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{MaxRawMessages: 2, PreserveRecent: 2}))

	// MaxRaw + PreserveRecent = 4 messages stay raw; folding starts once
	// the conversation passes 4 text messages.
	for i := 0; i < 4; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, "msg"),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.nodes) != 0 {
		t.Fatalf("want no fold at raw+preserve boundary, got %d nodes", len(store.nodes))
	}

	turn := memory.Turn{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		IdempotencyKey: "t4",
		Messages: []message.Message{
			message.NewTextMessage(message.RoleUser, "fifth"),
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

func TestAssemblyFoldReplacesNodeNoAccumulation(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{MaxRawMessages: 1, PreserveRecent: 1}))

	// Every turn past the raw+preserve boundary re-folds; the level-0 node
	// must be replaced in place so rows never accumulate.
	for i := 0; i < 10; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("msg-%02d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.nodes) != 1 {
		t.Fatalf("want exactly 1 level-0 node after many folds, got %d", len(store.nodes))
	}
	// The summary must have advanced with the conversation (no freeze).
	node := store.nodes[0]
	if !strings.Contains(node.Content.Text(), "msg-07") {
		t.Fatalf("summary must advance with the conversation, got %q", node.Content.Text())
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

func TestAssemblyCondensesFullSummary(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	calls := 0
	gen := func(
		_ context.Context,
		req inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		calls++
		if req.Input.Role != inference.InputRoleUser {
			t.Fatalf("input role = %q", req.Input.Role)
		}
		return inference.GenerateResponse{
			Message:      message.NewTextMessage(message.RoleAssistant, "CONDENSED"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(Policy{
			MaxRawMessages:  2,
			PreserveRecent:  2,
			MaxSummaryBytes: 64,
		}),
		withGenerate(gen))

	// 12 text messages make 8 foldable once the raw+preserve band is
	// reserved. 8 * "user: mN\n" (72 bytes) exceeds the 64-byte budget,
	// so the rolling window drops the oldest message — the trigger that
	// fires LLM condensation.
	for i := 0; i < 12; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("condense calls = %d, want 1", calls)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	if got := store.nodes[0].Content.Text(); got != "CONDENSED" {
		t.Fatalf("node text = %q, want condensed", got)
	}
	if store.nodes[0].Metadata["algorithm"] != "summary_llm_condense" {
		t.Fatalf("algorithm metadata = %v", store.nodes[0].Metadata["algorithm"])
	}

	// Re-folding the same window must not re-run the generation: the
	// raw-text hash guard short-circuits the unchanged fold.
	if err := a.fold(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("condense calls after no-op fold = %d, want 1", calls)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes after no-op fold = %d, want 1", len(store.nodes))
	}
}

func TestAssemblyCondenseMergesPreviousSummary(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	var prompts []string
	gen := func(
		_ context.Context,
		req inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		prompts = append(prompts, req.Input.Content.Text())
		return inference.GenerateResponse{
			Message:      message.NewTextMessage(message.RoleAssistant, fmt.Sprintf("C%d", len(prompts))),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(Policy{
			MaxRawMessages:  2,
			PreserveRecent:  2,
			MaxSummaryBytes: 24,
		}),
		withGenerate(gen))

	// 8 text messages with a 24-byte budget: the rolling window drops the
	// oldest foldable messages, firing condensation at the 7th message and
	// again at the 8th. The second condensation must merge the first
	// condensed output, so the facts of the dropped messages survive.
	for i := 0; i < 8; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(prompts) < 2 {
		t.Fatalf("condense calls = %d, want >= 2", len(prompts))
	}
	if !strings.Contains(prompts[1], "C1") {
		t.Fatalf("second condense input %q must merge previous summary C1", prompts[1])
	}
	if !strings.Contains(prompts[1], "m2") || !strings.Contains(prompts[1], "m3") {
		t.Fatalf("second condense input %q must contain the new raw window", prompts[1])
	}
}

func TestAssemblyCondenseFailureFallsBackToBuffer(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	calls := 0
	gen := func(
		context.Context,
		inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		calls++
		return inference.GenerateResponse{}, fmt.Errorf("provider down")
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(Policy{
			MaxRawMessages:  2,
			PreserveRecent:  2,
			MaxSummaryBytes: 24,
		}),
		withGenerate(gen))

	// Condensation fails at the 7th message (first drop) and again at the
	// 8th (window advance); the node must keep the raw buffer text and the
	// algorithm stays summary_buffer so memory is never lost.
	for i := 0; i < 8; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("condense calls = %d, want 2 (one per window advance)", calls)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	node := store.nodes[0]
	if got := node.Content.Text(); got != "user: m2\nuser: m3" {
		t.Fatalf("node text = %q, want buffer fallback text", got)
	}
	if node.Metadata["algorithm"] != "summary_buffer" {
		t.Fatalf("algorithm = %v, want summary_buffer on failure", node.Metadata["algorithm"])
	}
}

func TestAssemblyNoCondenseWhenNothingDropped(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	calls := 0
	gen := func(
		context.Context,
		inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		calls++
		return inference.GenerateResponse{
			Message:      message.NewTextMessage(message.RoleAssistant, "CONDENSED"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(Policy{
			MaxRawMessages:  2,
			PreserveRecent:  2,
			MaxSummaryBytes: 4096,
		}),
		withGenerate(gen))

	// The 24-byte budget of the other tests is replaced by a generous one:
	// folding happens but nothing is dropped, so LLM condensation must not
	// fire — small conversations stay pure buffer fold at zero cost.
	for i := 0; i < 6; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatalf("condense calls = %d, want 0 (nothing dropped)", calls)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	if store.nodes[0].Metadata["algorithm"] != "summary_buffer" {
		t.Fatalf("algorithm = %v, want summary_buffer", store.nodes[0].Metadata["algorithm"])
	}
}

func TestAssemblyCondenseCapsOutputToBudget(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	gen := func(
		context.Context,
		inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		return inference.GenerateResponse{
			Message: message.NewTextMessage(
				message.RoleAssistant,
				strings.Repeat("x", 1000),
			),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(Policy{
			MaxRawMessages:  2,
			PreserveRecent:  2,
			MaxSummaryBytes: 64,
		}),
		withGenerate(gen))

	for i := 0; i < 12; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	if got := store.nodes[0].Content.Text(); len(got) > 64 {
		t.Fatalf("condensed output not capped: %d bytes > 64", len(got))
	}
}

func TestAssemblyFoldAndContextNeverFullLoad(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{fakeTurnStore: &fakeTurnStore{msgs: map[string][]message.Message{}}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{
		MaxRawMessages: 2, PreserveRecent: 2, MaxSummaryBytes: 64,
	}))

	// 30 turns: every fold must go through CountMessages plus bounded range
	// loads, never a full LoadMessages of the growing conversation.
	for i := 0; i < 30; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("msg-%02d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if store.fullLoads != 0 {
		t.Fatalf("fold used %d full LoadMessages calls, want 0 (incremental)", store.fullLoads)
	}
	if store.countCalls == 0 || store.rangeLoads == 0 {
		t.Fatalf("fold must use CountMessages + LoadMessagesRange (count=%d range=%d)",
			store.countCalls, store.rangeLoads)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1 rolling node", len(store.nodes))
	}

	// Context must also avoid the full scan.
	store.fullLoads = 0
	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 0, MaxChars: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.fullLoads != 0 {
		t.Fatalf("context used %d full LoadMessages calls, want 0", store.fullLoads)
	}
	if len(res.Items) == 0 {
		t.Fatal("want context items")
	}
}

func TestAssemblyFoldTailMatchesFullBufferFold(t *testing.T) {
	ctx := context.Background()
	pol := Policy{MaxRawMessages: 4, PreserveRecent: 2, MaxSummaryBytes: 128}
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	a := NewAssembly(store, WithAssemblyPolicy(pol))

	// 40 messages: the foldable region far exceeds the byte budget, so the
	// tail path loads only the newest foldable messages.
	for i := 0; i < 40; i++ {
		turn := memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%02d", i)),
			},
		}
		if err := a.CommitTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	got := store.nodes[0]

	// The full-load BufferFold over the same messages must produce the
	// identical summary: same text, same source IDs, same drop count, same
	// stable node ID.
	want, err := BufferFold(pol, "c1", store.msgs["c1"], nil, got.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if want == nil {
		t.Fatal("full-load BufferFold returned nil")
	}
	if got.Content.Text() != want.Content.Text() {
		t.Fatalf("tail fold text %q != full fold text %q", got.Content.Text(), want.Content.Text())
	}
	if !slices.Equal(got.SourceIDs, want.SourceIDs) {
		t.Fatalf("tail fold source ids %v != full fold %v", got.SourceIDs, want.SourceIDs)
	}
	if got.ID != want.ID {
		t.Fatalf("tail fold id %s != full fold id %s", got.ID, want.ID)
	}
	gotDropped, _ := got.Metadata["dropped_message_count"].(int)
	wantDropped, _ := want.Metadata["dropped_message_count"].(int)
	if gotDropped != wantDropped {
		t.Fatalf("tail fold dropped %d != full fold dropped %d", gotDropped, wantDropped)
	}
}

func TestAssemblyContextLoadsOnlyRawWindow(t *testing.T) {
	ctx := context.Background()
	msgs := make([]message.Message, 0, 50)
	for i := 0; i < 50; i++ {
		msgs = append(msgs, message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%02d", i)))
	}
	store := &recordingStore{fakeTurnStore: &fakeTurnStore{
		msgs: map[string][]message.Message{"c1": msgs},
	}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{
		MaxRawMessages: 4, PreserveRecent: 2,
	}))

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 0, MaxChars: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.fullLoads != 0 {
		t.Fatalf("context used %d full LoadMessages calls, want 0", store.fullLoads)
	}
	// Raw window = the last MaxRaw + PreserveRecent = 6 messages:
	// original indices [44, 49].
	if len(store.ranges) != 1 || store.ranges[0] != [2]int{44, 49} {
		t.Fatalf("ranges = %v, want [[44 49]]", store.ranges)
	}
	if len(res.Items) != 6 {
		t.Fatalf("items = %d, want 6 raw messages", len(res.Items))
	}
}

func TestAssemblyReplayFullHistoryContext(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]message.Message{}}
	a := NewAssembly(store, WithReplayFullHistory(true))
	if !a.ReplayFullHistory() {
		t.Fatal("ReplayFullHistory must be true when configured")
	}
	store.msgs["s-1"] = []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleAssistant, "hi there"),
		message.NewTextMessage(message.RoleTool, "tool output"),
	}

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "s-1",
		Budget:         memory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("replay items = %d, want all 3 messages", len(res.Items))
	}
	wantRoles := []message.Role{
		message.RoleUser, message.RoleAssistant, message.RoleTool,
	}
	for i, want := range wantRoles {
		item := res.Items[i]
		if item.Kind != memory.ContextRawMessage {
			t.Fatalf("item %d kind = %v, want raw message", i, item.Kind)
		}
		if item.MessageRole != want {
			t.Fatalf("item %d role = %q, want %q", i, item.MessageRole, want)
		}
	}

	// An explicit budget is still honored.
	bounded, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "s-1",
		Budget:         memory.Budget{MaxItems: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Items) != 2 || !bounded.Truncated {
		t.Fatalf("bounded replay = %d items, truncated=%v; want 2/true",
			len(bounded.Items), bounded.Truncated)
	}
}
