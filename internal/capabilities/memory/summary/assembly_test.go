package summary

import (
	"context"
	"sync"
	"testing"

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

type fakeTurnStore struct {
	mu   sync.Mutex
	msgs map[string][]StoredMessage
}

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

func TestAssemblyRejectsDocuments(t *testing.T) {
	a := NewAssembly(&fakeTurnStore{msgs: map[string][]StoredMessage{}})
	err := a.PutDocument(context.Background(), memory.Document{})
	if err == nil {
		t.Fatal("want error for document sink")
	}
}

// TestAssemblyContextReplaysTheTranscript pins the only history policy: the
// context is the transcript as stored, with no summary-node window on top.
func TestAssemblyContextReplaysTheTranscript(t *testing.T) {
	ctx := context.Background()
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	a := NewAssembly(store)
	if !a.ReplayFullHistory() {
		t.Fatal("the replay is the only history policy")
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

func TestAssemblyReplayReusesTheRenderedPrefix(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	store := &recordingStore{fakeTurnStore: fake}
	a := NewAssembly(store)
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
	a := NewAssembly(store)
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
	a := NewAssembly(store)
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
// transcript can be tens of megabytes, and four of those resident would be the
// memory cost the cache is supposed to trade for churn.

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
		t.Fatal("an entry larger than the budget was dropped; the next turn would decode it again")
	}
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
	cache.drop("s-missing")
	if cache.chars != 0 {
		t.Fatalf("chars after a missing drop = %d, want 0", cache.chars)
	}
}
