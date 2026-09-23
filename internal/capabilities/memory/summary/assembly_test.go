package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

// storedRows assigns transcript coordinates to fixture messages: seqs
// count up from zero, the way the session store appends them.
func storedRows(msgs ...message.Message) []StoredMessage {
	out := make([]StoredMessage, 0, len(msgs))
	for i, msg := range msgs {
		out = append(out, StoredMessage{
			Seq:     int64(i),
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return out
}

// storedValues is storedRows for a message list built elsewhere (a row
// slice a fixture assembled before assigning it).
func storedSlice(msgs []message.Message) []StoredMessage {
	return storedRows(msgs...)
}

type fakeTurnStore struct {
	msgs  map[string][]StoredMessage
	nodes []SummaryNode
}

func (f *fakeTurnStore) AppendMessages(_ context.Context, conversationID, _ string, msgs []message.Message) error {
	rows := f.msgs[conversationID]
	next := int64(len(rows))
	for _, msg := range msgs {
		rows = append(rows, StoredMessage{
			Seq:     next,
			Role:    msg.Role,
			Content: msg.Content,
		})
		next++
	}
	f.msgs[conversationID] = rows
	return nil
}

// recordingStore wraps fakeTurnStore and records which load path the
// assembly takes, so tests can assert the incremental fold and context
// never fall back to a full conversation scan.
type recordingStore struct {
	*fakeTurnStore
	fullLoads int
	backLoads int
	// oldest is the oldest Seq any LoadBack walked back to.
	oldest     int64
	walkedBack bool
}

func (r *recordingStore) LoadAll(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	r.fullLoads++
	return r.fakeTurnStore.LoadAll(ctx, conversationID)
}

func (r *recordingStore) LoadBack(ctx context.Context, conversationID string, beforeSeq int64, n int) ([]StoredMessage, error) {
	r.backLoads++
	page, err := r.fakeTurnStore.LoadBack(ctx, conversationID, beforeSeq, n)
	if len(page) > 0 && (!r.walkedBack || page[0].Seq < r.oldest) {
		r.walkedBack = true
		r.oldest = page[0].Seq
	}
	return page, err
}

func (f *fakeTurnStore) MaxSeq(_ context.Context, conversationID string) (int64, error) {
	return int64(len(f.msgs[conversationID])) - 1, nil
}

func (f *fakeTurnStore) LoadAll(_ context.Context, conversationID string) ([]StoredMessage, error) {
	return f.msgs[conversationID], nil
}

func (f *fakeTurnStore) LoadBack(_ context.Context, conversationID string, beforeSeq int64, n int) ([]StoredMessage, error) {
	if n <= 0 {
		return nil, nil
	}
	rows := f.msgs[conversationID]
	var out []StoredMessage
	for i := len(rows) - 1; i >= 0 && len(out) < n; i-- {
		if rows[i].Seq < beforeSeq {
			out = append(out, rows[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
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

func (f *fakeTurnStore) DeleteSummaryNodesByID(_ context.Context, conversationID string, ids []string) error {
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	out := make([]SummaryNode, 0, len(f.nodes))
	for _, n := range f.nodes {
		if n.ThreadID == conversationID && drop[n.ID] {
			continue
		}
		out = append(out, n)
	}
	f.nodes = out
	return nil
}

func TestAssemblyCommitTurnFoldsOverWindow(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
	a := NewAssembly(&fakeTurnStore{msgs: map[string][]StoredMessage{}})
	err := a.PutDocument(context.Background(), memory.Document{})
	if err == nil {
		t.Fatal("want error for document sink")
	}
}

// TestAssemblyRetiresForeignGenerationNodes covers the upgrade path: a
// node written before the identity change carries source ids that are
// positions in a load order, not transcript coordinates. It covers
// nothing the reader can map, so Context ignores it, the next fold
// deletes it, and the fold that replaces it holds a coverage set that
// really is a subset of the transcript's seqs.
func TestAssemblyRetiresForeignGenerationNodes(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	// A node from the previous build: no identity marker, a position
	// hash as its source id, and enough raw rows beside it that the next
	// turn has something to fold.
	store.nodes = append(store.nodes, SummaryNode{
		ID:        "node-from-old-build",
		ThreadID:  "c1",
		Level:     0,
		SourceIDs: []string{"9f2c…"}, // sha256 of a load position
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "old summary"}}},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	})
	store.msgs["c1"] = storedRows(
		message.NewTextMessage(message.RoleUser, "row 0"),
		message.NewTextMessage(message.RoleUser, "row 1"),
		message.NewTextMessage(message.RoleUser, "row 2"),
	)
	a := NewAssembly(store, WithAssemblyPolicy(Policy{MaxRawMessages: 1, PreserveRecent: 1}))

	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 8, MaxChars: 1 << 16},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range res.Items {
		if item.Kind == memory.ContextSummary {
			t.Fatalf("foreign-generation summary reached the window: %+v", item)
		}
	}

	turn := memory.Turn{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		IdempotencyKey: "t4",
		Messages: []message.Message{
			message.NewTextMessage(message.RoleUser, "row 3"),
		},
	}
	if err := a.CommitTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %+v, want the foreign node retired and one built", store.nodes)
	}
	node := store.nodes[0]
	if !node.CurrentGeneration() {
		t.Fatalf("rebuilt node generation = %q, want %q", node.Generation(), IdentitySeqV1)
	}
	seqs := map[string]bool{}
	for _, row := range store.msgs["c1"] {
		seqs[SourceID(row.Seq)] = true
	}
	for _, id := range node.SourceIDs {
		if !seqs[id] {
			t.Fatalf("source id %q is not a transcript row; coverage = %v",
				id, node.SourceIDs)
		}
	}
}

func TestAssemblyContextReturnsRecentRawMessages(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{
		"c1": storedRows(
			message.NewTextMessage(message.RoleUser, "first"),
			message.NewTextMessage(message.RoleAssistant, "answer one"),
		),
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
		msgs: map[string][]StoredMessage{
			"c1": storedRows(first, message.NewTextMessage(message.RoleAssistant, "answer one")),
		},
		nodes: []SummaryNode{{
			ID:        "node-1",
			ThreadID:  "c1",
			Level:     0,
			SourceIDs: []string{SourceID(0)},
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "summary of first"},
			}},
			Metadata: map[string]any{IdentityKey: IdentitySeqV1},
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{
		"c1": storedRows(
			message.NewTextMessage(message.RoleUser, "first"),
			message.NewTextMessage(message.RoleUser, "second"),
			message.NewTextMessage(message.RoleUser, "third"),
		),
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	calls := 0
	gen := func(
		_ context.Context,
		req inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		calls++
		if req.Input.Role != inference.InputRoleUser {
			t.Errorf("input role = %q", req.Input.Role)
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
		a.condenseWG.Wait()
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
		a.condenseWG.Wait()
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
		a.condenseWG.Wait()
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
	a.condenseWG.Wait()
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	if got := store.nodes[0].Content.Text(); len(got) > 64 {
		t.Fatalf("condensed output not capped: %d bytes > 64", len(got))
	}
}

func TestAssemblyFoldAndContextNeverFullLoad(t *testing.T) {
	ctx := context.Background()
	store := &recordingStore{fakeTurnStore: &fakeTurnStore{msgs: map[string][]StoredMessage{}}}
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
		t.Fatalf("fold used %d full LoadAll calls, want 0 (incremental)", store.fullLoads)
	}
	if store.backLoads == 0 {
		t.Fatal("fold must walk the transcript tail with LoadBack")
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

// TestAssemblyContextPairLookbackBoundedOnDanglingResult verifies a
// dangling tool result (its call was never persisted) cannot turn
// Context into a full-history scan: pairPrefix walks backward at most
// pairLookbackMax rows and then gives up, keeping the context cost
// independent of conversation length.
func TestAssemblyContextPairLookbackBoundedOnDanglingResult(t *testing.T) {
	ctx := context.Background()
	msgs := make([]message.Message, 0, 200)
	for i := 0; i < 199; i++ {
		msgs = append(msgs, message.NewTextMessage(
			message.RoleUser, fmt.Sprintf("m%03d", i)))
	}
	msgs = append(msgs, message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "dangling", Content: message.NewTextContent("ok"),
			}},
		}},
	})
	store := &recordingStore{fakeTurnStore: &fakeTurnStore{msgs: map[string][]StoredMessage{
		"c1": storedSlice(msgs),
	}}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{
		MaxRawMessages: 2, PreserveRecent: 1,
	}))
	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: "c1",
		Budget:         memory.Budget{MaxItems: 0, MaxChars: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) == 0 {
		t.Fatal("want context items")
	}
	if store.fullLoads != 0 {
		t.Fatalf("context used %d full LoadAll calls, want 0", store.fullLoads)
	}
	if store.backLoads == 0 {
		t.Fatal("context must load a bounded raw window")
	}
	// The dangling result forces pairPrefix to walk backward; it must
	// stop pairLookbackMax rows before the boundary instead of reaching
	// seq 0 and re-scanning the whole conversation on every turn. The
	// window is the newest 3 rows, so its first seq is len(msgs)-3 and
	// the lookback may reach at most pairLookbackMax rows below it.
	if !store.walkedBack {
		t.Fatal("context must walk the transcript back")
	}
	if want := int64(len(msgs) - 3 - pairLookbackMax); store.oldest < want {
		t.Fatalf("pair lookback walked to seq %d, want no farther than %d",
			store.oldest, want)
	}
}

func TestAssemblyFoldTailMatchesFullBufferFold(t *testing.T) {
	ctx := context.Background()
	pol := Policy{MaxRawMessages: 4, PreserveRecent: 2, MaxSummaryBytes: 128}
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
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
		msgs: map[string][]StoredMessage{"c1": storedSlice(msgs)},
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
		t.Fatalf("context used %d full LoadAll calls, want 0", store.fullLoads)
	}
	// Raw window = the newest MaxRaw + PreserveRecent = 6 rows, i.e. seqs
	// [44, 49]; one backward walk must fetch exactly that window.
	if store.backLoads != 1 {
		t.Fatalf("context walked the transcript %d times, want one window walk",
			store.backLoads)
	}
	if store.oldest != 44 {
		t.Fatalf("window walked back to seq %d, want 44", store.oldest)
	}
	if len(res.Items) != 6 {
		t.Fatalf("items = %d, want 6 raw messages", len(res.Items))
	}
}

func TestAssemblyReplayFullHistoryContext(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	a := NewAssembly(store, WithReplayFullHistory(true))
	if !a.ReplayFullHistory() {
		t.Fatal("ReplayFullHistory must be true when configured")
	}
	store.msgs["s-1"] = storedRows(
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleAssistant, "hi there"),
		message.NewTextMessage(message.RoleTool, "tool output"),
	)

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

func TestAssemblyContextExtendsBoundaryForToolPair(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{
		"c1": storedRows(
			message.NewTextMessage(message.RoleUser, "hello"),
			message.Message{
				Role: message.RoleAssistant,
				Content: message.Content{Parts: []message.Part{
					message.ToolCallPart{Call: message.ToolCall{
						ID: "c1", Name: "fetch",
						Arguments: json.RawMessage(`{}`),
					}},
				}},
			},
			message.Message{
				Role: message.RoleTool,
				Content: message.Content{Parts: []message.Part{
					message.ToolResultPart{Result: message.ToolResult{
						CallID: "c1", Content: message.NewTextContent("ok"),
					}},
				}},
			},
			message.NewTextMessage(message.RoleUser, "next"),
			message.NewTextMessage(message.RoleAssistant, "done"),
		),
	}}
	// MaxRawMessages 2 + PreserveRecent 1 = window 3: without pair
	// awareness the boundary would land at index 2, cutting the
	// assistant call at index 1 from its tool result.
	a := NewAssembly(store, WithAssemblyPolicy(Policy{
		MaxRawMessages: 2, PreserveRecent: 1,
	}))
	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "c1",
		Budget:         memory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 4 {
		t.Fatalf("items = %d, want 4 (call + result + 2 raw tail)", len(res.Items))
	}
	wantRoles := []message.Role{
		message.RoleAssistant, message.RoleTool,
		message.RoleUser, message.RoleAssistant,
	}
	for i, want := range wantRoles {
		if res.Items[i].Kind != memory.ContextRawMessage ||
			res.Items[i].MessageRole != want {
			t.Fatalf("item %d = %+v, want raw %s",
				i, res.Items[i], want)
		}
	}
	if len(res.Items[0].Content.Parts) != 1 {
		t.Fatalf("call item parts = %d, want one tool_call",
			len(res.Items[0].Content.Parts))
	}
}

func TestAssemblyFoldKeepsToolPairRaw(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{
		"c1": storedRows(
			message.NewTextMessage(message.RoleUser, "hello"),
			message.Message{
				Role: message.RoleAssistant,
				Content: message.Content{Parts: []message.Part{
					message.ToolCallPart{Call: message.ToolCall{
						ID: "c1", Name: "fetch",
						Arguments: json.RawMessage(`{}`),
					}},
				}},
			},
			message.Message{
				Role: message.RoleTool,
				Content: message.Content{Parts: []message.Part{
					message.ToolResultPart{Result: message.ToolResult{
						CallID: "c1", Content: message.NewTextContent("ok"),
					}},
				}},
			},
			message.NewTextMessage(message.RoleUser, "next"),
			message.NewTextMessage(message.RoleAssistant, "done"),
		),
	}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{
		MaxRawMessages: 2, PreserveRecent: 1,
	}))
	if err := a.FoldOnly(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want one rolling summary", len(store.nodes))
	}
	res, err := a.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "c1",
		Budget:         memory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raws []memory.ContextItem
	for _, item := range res.Items {
		if item.Kind == memory.ContextRawMessage {
			raws = append(raws, item)
		}
	}
	if len(raws) != 4 {
		t.Fatalf("raw items = %d, want 4 (call+result+tail)",
			len(raws))
	}
	if raws[0].MessageRole != message.RoleAssistant ||
		raws[1].MessageRole != message.RoleTool {
		t.Fatalf("roles = %s/%s, want assistant/tool pair preserved",
			raws[0].MessageRole, raws[1].MessageRole)
	}
}
