package summary

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
)

// nodeWriteLog wraps the fake store and records what every node upsert
// carried, so a test can tell "never written" from "written and then
// overwritten".
type nodeWriteLog struct {
	*fakeTurnStore
	mu     sync.Mutex
	writes []string
}

func (s *nodeWriteLog) UpsertSummaryNode(
	ctx context.Context, node SummaryNode,
) error {
	s.mu.Lock()
	s.writes = append(s.writes, node.Content.Text())
	s.mu.Unlock()
	return s.fakeTurnStore.UpsertSummaryNode(ctx, node)
}

// CondenseSummaryNode records the swap the same way upserts are
// recorded: only a condensation that actually landed counts as written,
// so "the stale condensation was written" stays a question about what
// reached the store.
func (s *nodeWriteLog) CondenseSummaryNode(
	ctx context.Context, want, replacement SummaryNode,
) (bool, error) {
	ok, err := s.fakeTurnStore.CondenseSummaryNode(ctx, want, replacement)
	if ok {
		s.mu.Lock()
		s.writes = append(s.writes, replacement.Content.Text())
		s.mu.Unlock()
	}
	return ok, err
}

func (s *nodeWriteLog) wrote(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.writes {
		if got == text {
			return true
		}
	}
	return false
}

// condensePolicy is the small-budget policy the freshly folded window
// trips: with MaxRawMessages 2 / PreserveRecent 2 the rolling window
// starts dropping foldable messages around the 7th commit, which is
// what fires the LLM condensation stage.
func condensePolicy() Policy {
	return Policy{MaxRawMessages: 2, PreserveRecent: 2, MaxSummaryBytes: 24}
}

// TestAssemblyCondenseDropsStaleJob pins the freshness gate: the node id
// is stable, so a condensation that was computed from an older buffer
// must not overwrite the newer fold that landed while its generation
// ran. The stale job is dropped and the queued newer one applies.
func TestAssemblyCondenseDropsStaleJob(t *testing.T) {
	store := &nodeWriteLog{fakeTurnStore: &fakeTurnStore{msgs: map[string][]StoredMessage{}}}
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	calls := 0
	gen := func(
		_ context.Context, _ inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			once.Do(func() { close(started) })
			// Park the first generation while a newer fold advances
			// the same rolling node underneath it.
			<-release
			return inference.GenerateResponse{
				Message: message.NewTextMessage(
					message.RoleAssistant, "STALE"),
				FinishReason: inference.FinishCompleted,
			}, nil
		}
		return inference.GenerateResponse{
			Message: message.NewTextMessage(
				message.RoleAssistant, "FRESH"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(condensePolicy()),
		withGenerate(gen))

	for i := 0; i < 7; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first condensation never started")
	}
	// The 8th message advances the window while that generation is
	// parked; its own condensation queues behind the gate.
	commit(t, a, store, "c1", "t7", "m7")
	close(release)
	a.workWG.Wait()

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("condense calls = %d, want 2", got)
	}
	if store.wrote("STALE") {
		t.Fatal("the stale condensation was written: the gate must drop it before the upsert")
	}
	if len(store.nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(store.nodes))
	}
	node := store.nodes[0]
	if text := node.Content.Text(); text != "FRESH" {
		t.Fatalf("node text = %q, want FRESH (the stale job must not overwrite the newer fold)", text)
	}
	if node.Metadata["algorithm"] != "summary_llm_condense" {
		t.Fatalf("algorithm metadata = %v", node.Metadata["algorithm"])
	}
}

// TestAssemblyCondensesOneAtATime pins the gate itself: however many
// folds race ahead, only one generation is ever in flight, so a burst
// of commits cannot spend a model call per window in parallel.
func TestAssemblyCondensesOneAtATime(t *testing.T) {
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	var inflight, peak, calls atomic.Int32
	gen := func(
		_ context.Context, _ inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		calls.Add(1)
		cur := inflight.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inflight.Add(-1)
		return inference.GenerateResponse{
			Message: message.NewTextMessage(
				message.RoleAssistant, "C"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(condensePolicy()),
		withGenerate(gen))

	for i := 0; i < 12; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
	}
	a.workWG.Wait()
	if calls.Load() < 2 {
		t.Fatalf("condense calls = %d, want at least 2", calls.Load())
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrent condensations = %d, want 1", got)
	}
}

// TestAssemblyCloseWaitsForCondense pins the teardown half: the resource
// lifecycle closes the assembly while a generation may still be in
// flight, and Close must not return before that write is done.
func TestAssemblyCloseWaitsForCondense(t *testing.T) {
	store := &fakeTurnStore{msgs: map[string][]StoredMessage{}}
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	gen := func(
		context.Context, inference.GenerateRequest,
	) (inference.GenerateResponse, error) {
		once.Do(func() { close(started) })
		<-release
		return inference.GenerateResponse{
			Message: message.NewTextMessage(
				message.RoleAssistant, "C"),
			FinishReason: inference.FinishCompleted,
		}, nil
	}
	a := NewAssembly(store,
		WithAssemblyPolicy(condensePolicy()),
		withGenerate(gen))
	for i := 0; i < 7; i++ {
		commit(t, a, store, "c1", fmt.Sprintf("t%d", i), fmt.Sprintf("m%d", i))
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the condensation never started")
	}

	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) while a condensation was in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the generation finished")
	}
}
