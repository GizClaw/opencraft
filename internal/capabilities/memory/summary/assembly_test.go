package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	mu    sync.Mutex
	msgs  map[string][]StoredMessage
	nodes []SummaryNode
	// writes counts every mutating store call, so a test can assert a
	// barrier held without reading stable ids back: a fold that ran
	// rewrites the same level-0 node in place, and a node count would
	// not move.
	writes int
}

// writeCount returns the number of mutating store calls so far.
func (f *fakeTurnStore) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

// appendRows writes one fixture turn to the transcript the way the
// session store appends one: seqs count up from zero. The assembly never
// writes — it reads what this fake holds — so seeding is the test's job.
func (f *fakeTurnStore) appendRows(conversationID string, msgs ...message.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
}

// recordingStore wraps fakeTurnStore and records which load path the
// assembly takes, so tests can assert the incremental fold and context
// never fall back to a full conversation scan.
type recordingStore struct {
	*fakeTurnStore
	fullLoads int
	backLoads int
	// afterLoads counts incremental reads and remembers the cursor they
	// asked for.
	afterLoads  int
	afterCursor int64
	// oldest is the oldest Seq any LoadBack walked back to.
	oldest     int64
	walkedBack bool
}

func (r *recordingStore) LoadAll(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	r.fullLoads++
	return r.fakeTurnStore.LoadAll(ctx, conversationID)
}

func (r *recordingStore) LoadAfter(
	ctx context.Context, conversationID string, afterSeq int64,
) ([]StoredMessage, error) {
	r.afterLoads++
	r.afterCursor = afterSeq
	return r.fakeTurnStore.LoadAfter(ctx, conversationID, afterSeq)
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.msgs[conversationID])) - 1, nil
}

func (f *fakeTurnStore) LoadAll(_ context.Context, conversationID string) ([]StoredMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msgs[conversationID], nil
}

func (f *fakeTurnStore) LoadAfter(
	_ context.Context, conversationID string, afterSeq int64,
) ([]StoredMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []StoredMessage
	for _, row := range f.msgs[conversationID] {
		if row.Seq > afterSeq {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeTurnStore) LoadBack(_ context.Context, conversationID string, beforeSeq int64, n int) ([]StoredMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	for i := range f.nodes {
		if f.nodes[i].ID == n.ID {
			f.nodes[i] = n
			return nil
		}
	}
	f.nodes = append(f.nodes, n)
	return nil
}

// CondenseSummaryNode mirrors the sqlite store's atomic gate: the swap
// lands only while the row still holds exactly the buffer the job
// condensed (content, coverage, write time).
func (f *fakeTurnStore) CondenseSummaryNode(
	_ context.Context, want, replacement SummaryNode,
) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.nodes {
		cur := f.nodes[i]
		if cur.ID != want.ID {
			continue
		}
		if cur.Content.Text() != want.Content.Text() ||
			!cur.UpdatedAt.Equal(want.UpdatedAt) ||
			!slices.Equal(cur.SourceIDs, want.SourceIDs) {
			return false, nil
		}
		f.writes++
		f.nodes[i] = replacement
		return true, nil
	}
	return false, nil
}

func (f *fakeTurnStore) ListSummaryNodes(_ context.Context, conversationID string) ([]SummaryNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []SummaryNode
	for _, n := range f.nodes {
		if n.ThreadID == conversationID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeTurnStore) DeleteSummaryNodes(_ context.Context, conversationID string, level int, keepID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
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

// rowWriter seeds a fixture transcript: the store under test either is a
// fakeTurnStore or wraps one.
type rowWriter interface {
	appendRows(conversationID string, msgs ...message.Message)
}

// commit is the production sequence in one call for tests about folding:
// the conversation's owner writes the turn to the transcript
// (capabilities/sessions, the fake store here) and the assembly's sink
// folds what is now on disk. CommitTurn writes nothing, so a test that
// seeded only through it would fold an empty conversation.
func commit(
	t *testing.T,
	a *Assembly,
	store rowWriter,
	conversationID, runKey, text string,
) {
	t.Helper()
	msg := message.NewTextMessage(message.RoleUser, text)
	store.appendRows(conversationID, msg)
	if err := a.CommitTurn(context.Background(), memory.Turn{
		Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: conversationID,
		IdempotencyKey: runKey,
		Messages:       []message.Message{msg},
	}); err != nil {
		t.Fatalf("commit %s/%s: %v", conversationID, runKey, err)
	}
}

func TestAssemblyCommitTurnFoldsOverWindow(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{MaxRawMessages: 2, PreserveRecent: 2}))

	// MaxRaw + PreserveRecent = 4 messages stay raw; folding starts once
	// the conversation passes 4 text messages.
	for i := 0; i < 4; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), "msg")
	}
	if len(store.nodes) != 0 {
		t.Fatalf("want no fold at raw+preserve boundary, got %d nodes", len(store.nodes))
	}

	commit(t, a, store, "c1", "t4", "fifth")
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
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	a := NewAssembly(store, WithAssemblyPolicy(Policy{MaxRawMessages: 1, PreserveRecent: 1}))

	// Every turn past the raw+preserve boundary re-folds; the level-0 node
	// must be replaced in place so rows never accumulate.
	for i := 0; i < 10; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("msg-%02d", i))
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

	commit(t, a, store, "c1", "t4", "row 3")
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
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
		a.workWG.Wait()
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
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
		a.workWG.Wait()
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
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
		a.workWG.Wait()
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
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
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
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
	}
	a.workWG.Wait()
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

	// 30 turns: every fold must go through MaxSeq plus bounded backward
	// loads, never a full LoadAll of the growing conversation.
	for i := 0; i < 30; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("msg-%02d", i))
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
	pol := Policy{MaxRawMessages: 4, PreserveRecent: 2, MaxSummaryBytes: 128}
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	a := NewAssembly(store, WithAssemblyPolicy(pol))

	// 40 messages: the foldable region far exceeds the byte budget, so the
	// tail path loads only the newest foldable messages.
	for i := 0; i < 40; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%02d", i))
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

// TestAssemblyCloseStopsScheduling pins the close barrier: once Close
// ran, a late commit still lands its transcript (that write happened
// before the fold) but no fold reaches the store and no condensation
// starts — the store is being torn down and a generation would outlive
// its reader. Skipping is not a failed turn: the summary is derived
// state, and reporting an error here would surface a teardown race as
// one.
func TestAssemblyCloseStopsScheduling(t *testing.T) {
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	var calls atomic.Int32
	gen := func(
		_ context.Context, _ inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		calls.Add(1)
		return inference.GenerateResponse{
			Message: message.NewTextMessage(
				message.RoleAssistant, "CONDENSED"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(condensePolicy()),
		withGenerate(gen))
	for i := 0; i < 7; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	before := calls.Load()
	beforeWrites := store.writeCount()
	for i := 7; i < 14; i++ {
		msg := message.NewTextMessage(message.RoleUser, fmt.Sprintf("m%d", i))
		store.appendRows("c1", msg)
		err := a.CommitTurn(context.Background(), memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: "c1",
			IdempotencyKey: fmt.Sprintf("t%d", i),
			Messages:       []message.Message{msg},
		})
		if err != nil {
			t.Fatalf("commit %d after Close: %v; the transcript write must still land", i, err)
		}
	}
	if after := calls.Load(); after != before {
		t.Fatalf("condensation calls %d -> %d after Close; new work must not start",
			before, after)
	}
	// A fold rewrites the same stable-id node in place, so the node
	// count cannot move; the write counter is what pins "no fold
	// reached the store".
	if after := store.writeCount(); after != beforeWrites {
		t.Fatalf("store writes %d -> %d after Close; the fold must not touch the store",
			beforeWrites, after)
	}
}

// TestAssemblyCloseRacesFolds drives folds and Close concurrently: the
// closed bit and the wait group are one mutex-protected step, so the
// race can neither trip the WaitGroup misuse panic nor leave a
// condensation goroutine that outlives Close.
func TestAssemblyCloseRacesFolds(t *testing.T) {
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	gen := func(
		_ context.Context, _ inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		return inference.GenerateResponse{
			Message: message.NewTextMessage(
				message.RoleAssistant, "CONDENSED"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(condensePolicy()),
		withGenerate(gen))
	for i := 0; i < 7; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
	}

	emit := func(worker, i int) {
		msg := message.NewTextMessage(
			message.RoleUser, fmt.Sprintf("w%d-m%d", worker, i))
		// One conversation per worker keeps the folding windows
		// independent; CommitTurn folds whatever the fake store holds.
		conv := fmt.Sprintf("c%d", worker+2)
		store.appendRows(conv, msg)
		_ = a.CommitTurn(context.Background(), memory.Turn{
			Scope:          memory.Scope{RuntimeID: "rt", AgentID: "a"},
			ConversationID: conv,
			IdempotencyKey: fmt.Sprintf("w%d-t%d", worker, i),
			Messages:       []message.Message{msg},
		})
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				emit(w, i)
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = a.Close()
	}()
	wg.Wait()
}

// The replay prefix is cached: a turn pays for the rows it appended, not
// for the whole transcript. These cases pin the three paths — nothing new,
// an appended tail, and a transcript that shrank under the cache — plus
// the rule that a budgeted request never shares the prefix.

func TestAssemblyReplayReusesTheRenderedPrefix(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	store := &recordingStore{fakeTurnStore: fake}
	a := NewAssembly(store, WithReplayFullHistory(true))
	fake.appendRows("s-1",
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleAssistant, "hi there"),
	)
	request := memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "s-1",
	}

	first, err := a.Context(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if store.fullLoads != 1 || store.afterLoads != 0 {
		t.Fatalf("first render: full=%d after=%d, want one full read",
			store.fullLoads, store.afterLoads)
	}

	// Nothing appended: no read at all, same items.
	again, err := a.Context(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if store.fullLoads != 1 || store.afterLoads != 0 {
		t.Fatalf("unchanged transcript: full=%d after=%d, want no new read",
			store.fullLoads, store.afterLoads)
	}
	if len(again.Items) != len(first.Items) {
		t.Fatalf("items = %d, want the cached %d", len(again.Items), len(first.Items))
	}

	// One turn appended: one incremental read from the cached cursor.
	fake.appendRows("s-1", message.NewTextMessage(message.RoleUser, "and again"))
	grown, err := a.Context(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if store.fullLoads != 1 || store.afterLoads != 1 {
		t.Fatalf("appended turn: full=%d after=%d, want one incremental read",
			store.fullLoads, store.afterLoads)
	}
	if store.afterCursor != int64(len(first.Items)-1) {
		t.Fatalf("incremental read started after seq %d, want %d",
			store.afterCursor, len(first.Items)-1)
	}
	if len(grown.Items) != len(first.Items)+1 {
		t.Fatalf("items = %d, want %d", len(grown.Items), len(first.Items)+1)
	}
	// The incremental result must be the one a full render would produce.
	want, err := fake.LoadAll(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(grown.Items) {
		t.Fatalf("full read has %d rows, incremental items %d",
			len(want), len(grown.Items))
	}
	for i, row := range want {
		if grown.Items[i].Sequence != uint64(row.Seq) {
			t.Fatalf("item %d seq = %d, want %d",
				i, grown.Items[i].Sequence, row.Seq)
		}
	}
}

func TestAssemblyReplayRerendersWhenTheTranscriptShrank(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	store := &recordingStore{fakeTurnStore: fake}
	a := NewAssembly(store, WithReplayFullHistory(true))
	fake.appendRows("s-1",
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleAssistant, "hi there"),
	)
	request := memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "s-1",
	}
	if _, err := a.Context(ctx, request); err != nil {
		t.Fatal(err)
	}

	// A deleted conversation (or one replaced by a shorter transcript):
	// the cached prefix describes rows that no longer exist.
	fake.mu.Lock()
	fake.msgs["s-1"] = storedRows(message.NewTextMessage(message.RoleUser, "new"))
	fake.mu.Unlock()
	res, err := a.Context(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if store.fullLoads != 2 || store.afterLoads != 0 {
		t.Fatalf("shrunk transcript: full=%d after=%d, want a full re-render",
			store.fullLoads, store.afterLoads)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want the rewritten transcript's 1 row", len(res.Items))
	}
}

func TestAssemblyReplayBudgetNeverUsesThePrefix(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	store := &recordingStore{fakeTurnStore: fake}
	a := NewAssembly(store, WithReplayFullHistory(true))
	for i := 0; i < 4; i++ {
		fake.appendRows("s-1", message.NewTextMessage(message.RoleUser, "turn"))
	}
	request := memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "rt"},
		ConversationID: "s-1",
	}
	if _, err := a.Context(ctx, request); err != nil {
		t.Fatal(err)
	}
	bounded := request
	bounded.Budget = memory.Budget{MaxItems: 2}
	res, err := a.Context(ctx, bounded)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("a budgeted replay must report the truncation")
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want the budget's 2", len(res.Items))
	}
	if store.afterLoads != 0 {
		t.Fatalf("budgeted request used the prefix cache (%d incremental reads)",
			store.afterLoads)
	}
}

// The replay cache is bounded in bytes as well as in conversations: a
// transcript can be tens of megabytes, and four of those resident is the
// memory cost the cache was supposed to trade for churn. These cases pin the
// budget, the accounting that follows it, and the one entry that survives a
// budget smaller than itself.

func replayEntryWithChars(chars int, seq int64) *replayEntry {
	return &replayEntry{
		seq:   seq,
		chars: chars,
		items: []memory.ContextItem{{Content: message.NewTextContent("x")}},
	}
}

func TestReplayCacheEvictsByCharBudget(t *testing.T) {
	cache := newReplayCache(4)
	cache.maxChars = 100
	cache.put("s-1", replayEntryWithChars(60, 10))
	cache.put("s-2", replayEntryWithChars(60, 20))
	if _, ok := cache.state("s-1"); ok {
		t.Fatal("the oldest entry survived a budget overrun")
	}
	kept, ok := cache.state("s-2")
	if !ok || kept.seq != 20 {
		t.Fatalf("newest entry = %+v, %v; want it kept", kept, ok)
	}
	if cache.chars != 60 {
		t.Fatalf("cache chars = %d, want the surviving entry's 60", cache.chars)
	}
}

func TestReplayCacheKeepsOneEntryLargerThanTheBudget(t *testing.T) {
	cache := newReplayCache(4)
	cache.maxChars = 100
	cache.put("s-big", replayEntryWithChars(500, 5))
	if _, ok := cache.state("s-big"); !ok {
		t.Fatal("an entry larger than the budget was dropped; the reader would re-render it every turn")
	}
	// The next conversation still evicts the oversized one rather than
	// stacking another over-budget entry on top of it.
	cache.put("s-2", replayEntryWithChars(60, 6))
	if _, ok := cache.state("s-big"); ok {
		t.Fatal("the oversized entry was kept once another conversation arrived")
	}
}

func TestReplayCacheAccountingFollowsReplacementAndDrop(t *testing.T) {
	cache := newReplayCache(4)
	cache.maxChars = 1000
	cache.put("s-1", replayEntryWithChars(300, 1))
	cache.put("s-1", replayEntryWithChars(100, 2))
	if cache.chars != 100 {
		t.Fatalf("chars after replacing an entry = %d, want 100", cache.chars)
	}
	cache.drop("s-1")
	if cache.chars != 0 {
		t.Fatalf("chars after drop = %d, want 0", cache.chars)
	}
	// A drop of something that was never cached changes nothing.
	cache.drop("s-missing")
	if cache.chars != 0 {
		t.Fatalf("chars after a missing drop = %d, want 0", cache.chars)
	}
}
