package review

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	yamlv4 "go.yaml.in/yaml/v4"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// fakeUsage records the usage the review reports, so tests can assert a
// background call is never invisible.
type fakeUsage struct {
	mu  sync.Mutex
	got []inference.Usage
}

func (f *fakeUsage) ReportUsage(_ context.Context, u inference.Usage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, u)
}

func (f *fakeUsage) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

// newUserHandle opens a migrated user database shared by the memory and
// queue stores in these tests.
func newUserHandle(t *testing.T) *db.DB {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	return handle
}

func newMemoryStore(t *testing.T) *userstore.Store {
	t.Helper()
	store, err := userstore.Attach(newUserHandle(t))
	if err != nil {
		t.Fatalf("attach memory: %v", err)
	}
	return store
}

func newQueueStore(t *testing.T) *reviewstore.Store {
	t.Helper()
	store, err := reviewstore.Attach(newUserHandle(t))
	if err != nil {
		t.Fatalf("attach queue: %v", err)
	}
	return store
}

// newRouter builds the concrete *route.Router OnRunEnd requires to
// proceed. Its targets are irrelevant: the review path is driven through
// the generateFn seam, so the router is never actually called.
func newRouter(t *testing.T) *route.Router {
	t.Helper()
	router, err := route.New(
		&inference.Assembly{},
		route.Policy{
			Generate: []route.Pool{{
				Tier: "default",
				Targets: []route.Target{{Model: model.ModelRef{
					ID: model.ModelID{
						Provider: "deepseek",
						Name:     "deepseek-v4-flash",
					},
				}}},
			}},
		}.Selectors(&inference.Assembly{}),
	)
	if err != nil {
		t.Fatalf("route.New: %v", err)
	}
	return router
}

func enabledCfg() config.ReviewConfig {
	return config.ReviewConfig{
		Enabled: true, EveryTurns: 5, MinToolCalls: 0, OnFailure: true,
		MaxSuggestions: 3, TimeoutSeconds: 90,
	}
}

// newObserver assembles the hook with a real memory store, a real queue
// and a concrete router, the shape the deploy graph hands it.
func newObserver(
	t *testing.T, cfg config.ReviewConfig,
) (*Observer, *reviewstore.Store, *userstore.Store, *fakeUsage) {
	t.Helper()
	handle := newUserHandle(t)
	mem, err := userstore.Attach(handle)
	if err != nil {
		t.Fatalf("attach memory: %v", err)
	}
	queue, err := reviewstore.Attach(handle)
	if err != nil {
		t.Fatalf("attach queue: %v", err)
	}
	usage := &fakeUsage{}
	o := &Observer{}
	o.queue = &reviewstore.Binding{Queue: queue, Config: cfg}
	o.memory = &userstore.Binding{
		Memory: mem,
		Config: config.UserMemoryConfig{Enabled: true, InjectMaxItems: 12, InjectMaxChars: 2048},
	}
	o.router = newRouter(t)
	o.usage = usage
	o.settings = Settings{WorkDir: t.TempDir()}
	o.turns = map[string]int{}
	o.inflight = map[string]bool{}
	return o, queue, mem, usage
}

// chatTurn is a completed turn with a user request and an answer.
func chatTurn() *agent.Result {
	return &agent.Result{Status: agent.StatusCompleted, Messages: []message.Message{
		message.NewTextMessage(message.RoleUser, "do the thing"),
		message.NewTextMessage(message.RoleAssistant, "done"),
	}}
}

// toolTurn is a completed turn whose message list carries n tool results.
func toolTurn(n int) *agent.Result {
	msgs := []message.Message{message.NewTextMessage(message.RoleUser, "do the thing")}
	for i := 0; i < n; i++ {
		msgs = append(msgs, message.Message{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1", Content: message.NewTextContent("ok"),
				}},
			}},
		})
	}
	msgs = append(msgs, message.NewTextMessage(message.RoleAssistant, "done"))
	return &agent.Result{Status: agent.StatusCompleted, Messages: msgs}
}

// answer is the canned model reply the generateFn seam returns.
func answer(text string) inference.GenerateResponse {
	return inference.GenerateResponse{
		Message: message.NewTextMessage(message.RoleAssistant, text),
		Usage:   inference.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

func TestReviewDisabledRecursionGuard(t *testing.T) {
	o, _, _, _ := newObserver(t, enabledCfg())
	normal := agent.Identity{ConversationID: "s-1"}

	if o.reviewDisabled(context.Background(), normal) {
		t.Fatal("a normal run must be reviewable")
	}
	if !o.reviewDisabled(WithReviewContext(context.Background()), normal) {
		t.Fatal("the review's own context must be refused (IsReviewRun)")
	}
	if !o.reviewDisabled(context.Background(), agent.Identity{ConversationID: "ctx-abc"}) {
		t.Fatal("an ephemeral ctx- conversation must be refused")
	}

	off, _, _, _ := newObserver(t, config.ReviewConfig{Enabled: false})
	if !off.reviewDisabled(context.Background(), normal) {
		t.Fatal("a disabled review must be refused")
	}
}

func TestClaimReleaseIsSingleFlight(t *testing.T) {
	o, _, _, _ := newObserver(t, enabledCfg())
	if !o.claim("c") {
		t.Fatal("first claim must win")
	}
	if o.claim("c") {
		t.Fatal("a second claim of the same conversation must lose")
	}
	o.release("c")
	if !o.claim("c") {
		t.Fatal("the slot must be reusable after release")
	}
	o.release("c")
}

func TestCountTurnCadence(t *testing.T) {
	o, _, _, _ := newObserver(t, enabledCfg())
	for turn := 1; turn <= 2; turn++ {
		if o.countTurn("c", 3) {
			t.Fatalf("turn %d must not land on the cadence", turn)
		}
	}
	if !o.countTurn("c", 3) {
		t.Fatal("the third turn must land on a three-turn cadence")
	}
	if o.countTurn("c", 0) {
		t.Fatal("a zero cadence never fires")
	}
}

func TestOnRunEndSkipsWhenDisabled(t *testing.T) {
	cfg := enabledCfg()
	cfg.Enabled = false
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	o.OnRunEnd(context.Background(), agent.Identity{ConversationID: "s-1"}, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("disabled review ran %d reviews", calls)
	}
}

func TestOnRunEndSkipsBelowMinToolCalls(t *testing.T) {
	cfg := enabledCfg()
	cfg.EveryTurns = 1
	cfg.MinToolCalls = 4
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1"}
	o.OnRunEnd(context.Background(), id, toolTurn(3))
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("a three-tool turn ran %d reviews, want none", calls)
	}
	// The floor is inclusive: exactly min_tool_calls is enough.
	o.OnRunEnd(context.Background(), id, toolTurn(4))
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("a four-tool turn ran %d reviews, want one", calls)
	}
}

func TestOnRunEndRunsOnCadence(t *testing.T) {
	cfg := enabledCfg()
	cfg.EveryTurns = 2
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1"}
	o.OnRunEnd(context.Background(), id, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("the first turn ran %d reviews, want none", calls)
	}
	o.OnRunEnd(context.Background(), id, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the second turn ran %d reviews, want one", calls)
	}
}

func TestOnRunEndRunsOnFailure(t *testing.T) {
	cfg := enabledCfg()
	cfg.EveryTurns = 100
	cfg.MinToolCalls = 100
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	failed := &agent.Result{Status: agent.StatusFailed, Messages: []message.Message{
		message.NewTextMessage(message.RoleUser, "do the thing"),
	}}
	o.OnRunEnd(context.Background(), agent.Identity{ConversationID: "s-1"}, failed)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("a failed turn ran %d reviews, want one", calls)
	}
}

// TestOnRunEndDoesNotCountTurnsThatLostTheSlot pins the cadence/slot
// interplay: a turn that loses the single-flight race is not a review
// opportunity, and counting it would slide the next scheduled review a
// full period later.
func TestOnRunEndDoesNotCountTurnsThatLostTheSlot(t *testing.T) {
	cfg := enabledCfg()
	cfg.EveryTurns = 2
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1"}
	// An in-flight review holds the slot.
	if !o.claim(id.ConversationID) {
		t.Fatal("claim")
	}
	o.OnRunEnd(context.Background(), id, chatTurn())
	o.release(id.ConversationID)
	// That turn lost the slot, so this one is still the first of the
	// period, not the second.
	o.OnRunEnd(context.Background(), id, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("the turn after the lost slot ran %d reviews, want none", calls)
	}
	o.OnRunEnd(context.Background(), id, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the third turn ran %d reviews in total, want one", calls)
	}
}

// TestOnRunEndFailureAdvancesTheCadence pins that a failure review —
// which runs regardless of the cadence — still advances the counter, so
// the next scheduled review is a full period away instead of firing on
// the very next turn.
func TestOnRunEndFailureAdvancesTheCadence(t *testing.T) {
	cfg := enabledCfg()
	cfg.EveryTurns = 2
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1"}
	failed := &agent.Result{Status: agent.StatusFailed, Messages: []message.Message{
		message.NewTextMessage(message.RoleUser, "do the thing"),
	}}
	o.OnRunEnd(context.Background(), id, failed)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the failed turn ran %d reviews, want one", calls)
	}
	o.OnRunEnd(context.Background(), id, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("failure + one turn ran %d reviews, want the cadence on turn two", calls)
	}
	o.OnRunEnd(context.Background(), id, chatTurn())
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("the third turn ran %d reviews in total, want no more", calls)
	}
}

func TestOnRunEndSkipsFailureWhenOnFailureOff(t *testing.T) {
	cfg := enabledCfg()
	cfg.OnFailure = false
	o, _, _, _ := newObserver(t, cfg)
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	failed := &agent.Result{Status: agent.StatusFailed, Messages: []message.Message{
		message.NewTextMessage(message.RoleUser, "do the thing"),
	}}
	o.OnRunEnd(context.Background(), agent.Identity{ConversationID: "s-1"}, failed)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("on_failure=false still ran %d reviews", calls)
	}
}

// TestReviewQueuesCandidatesAndReportsUsage pins the whole path: the
// prompt is built, the answer validated, one suggestion queued under a
// stable id, usage reported, and nothing written into long-term memory
// before the user accepts.
func TestReviewQueuesCandidatesAndReportsUsage(t *testing.T) {
	ctx := context.Background()
	o, queue, mem, usage := newObserver(t, enabledCfg())
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"prefers ripgrep","scope":"global","kind":"preference","reason":"stated"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(ctx, id, chatTurn(), 0); err != nil {
		t.Fatalf("review: %v", err)
	}

	rows, err := queue.List(ctx, reviewstore.StatusPending, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("queued = %d, want 1", len(rows))
	}
	if rows[0].ID != "rv-run-1-0" {
		t.Fatalf("id = %q, want the stable rv-run-1-0", rows[0].ID)
	}
	if rows[0].Kind != reviewstore.KindMemory {
		t.Fatalf("kind = %q, want memory", rows[0].Kind)
	}
	if usage.count() != 1 {
		t.Fatalf("usage reports = %d, want 1", usage.count())
	}

	// The suggestion is a proposal: nothing lands in memory until accept.
	facts, err := mem.List(ctx, userstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("memory = %+v, want it empty until the suggestion is accepted", facts)
	}
}

func TestReviewReplayDoesNotDuplicateSuggestions(t *testing.T) {
	ctx := context.Background()
	o, queue, _, _ := newObserver(t, enabledCfg())
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"prefers ripgrep"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	for i := 0; i < 2; i++ {
		if err := o.review(ctx, id, chatTurn(), 0); err != nil {
			t.Fatalf("review %d: %v", i, err)
		}
	}
	rows, err := queue.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("queued = %d, want the stable id to dedupe the replay", len(rows))
	}
}

// TestReviewKnowsDecidedSuggestions pins the queue-side dedupe: a
// candidate the user already decided on is not proposed again, which is
// what keeping the decided row in the queue is for.
func TestReviewKnowsDecidedSuggestions(t *testing.T) {
	ctx := context.Background()
	o, queue, _, _ := newObserver(t, enabledCfg())
	if _, _, err := queue.Create(ctx, reviewstore.Suggestion{
		ID:      "rv-run-0-0",
		Kind:    reviewstore.KindMemory,
		Payload: []byte(`{"text":"prefers ripgrep"}`),
		Status:  reviewstore.StatusDiscarded,
	}); err != nil {
		t.Fatal(err)
	}
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"prefers ripgrep"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(ctx, id, chatTurn(), 0); err != nil {
		t.Fatalf("review: %v", err)
	}
	rows, err := queue.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the discarded row alone", len(rows))
	}
}

func TestReviewBoundsSuggestionsToMax(t *testing.T) {
	cfg := enabledCfg()
	cfg.MaxSuggestions = 2
	o, queue, _, _ := newObserver(t, cfg)
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"one"},{"text":"two"},{"text":"three"},{"text":"four"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(context.Background(), id, chatTurn(), 0); err != nil {
		t.Fatal(err)
	}
	rows, err := queue.List(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("queued = %d, want the max_suggestions cap of 2", len(rows))
	}
}

// TestReviewDropsDuplicateCandidates pins the in-batch dedupe: the
// store's identity is the normalized text, so a review that proposes
// the same fact twice queues it once.
func TestReviewDropsDuplicateCandidates(t *testing.T) {
	cfg := enabledCfg()
	cfg.MaxSuggestions = 5
	o, queue, _, _ := newObserver(t, cfg)
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"dup"},{"text":"DUP."},{"text":"four"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(context.Background(), id, chatTurn(), 0); err != nil {
		t.Fatal(err)
	}
	rows, err := queue.List(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("queued = %d, want \"dup\" and \"four\" only", len(rows))
	}
}

func TestReviewSkipsFactsAlreadyStored(t *testing.T) {
	ctx := context.Background()
	o, queue, mem, _ := newObserver(t, enabledCfg())
	if _, err := mem.Add(ctx, userstore.Fact{
		Text: "prefers ripgrep", Scope: userstore.ScopeGlobal,
	}); err != nil {
		t.Fatal(err)
	}
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"prefers ripgrep"},{"text":"new durable fact"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(ctx, id, chatTurn(), 0); err != nil {
		t.Fatal(err)
	}
	rows, err := queue.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("queued = %d, want the already-stored fact dropped", len(rows))
	}
}

func TestReviewSkipsOverlongCandidate(t *testing.T) {
	ctx := context.Background()
	o, queue, _, _ := newObserver(t, enabledCfg())
	huge := strings.Repeat("a", userstore.MaxTextBytes+1)
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer(`{"memory":[{"text":"` + huge + `"}]}`), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(ctx, id, chatTurn(), 0); err != nil {
		t.Fatal(err)
	}
	rows, err := queue.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("queued = %d, want the over-limit candidate refused", len(rows))
	}
}

func TestReviewMalformedAnswerIsAnError(t *testing.T) {
	o, queue, _, _ := newObserver(t, enabledCfg())
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		return answer("no json here"), nil
	}
	id := agent.Identity{ConversationID: "s-1", RunID: "run-1"}
	if err := o.review(context.Background(), id, chatTurn(), 0); err == nil {
		t.Fatal("a malformed answer must surface as an error")
	}
	rows, err := queue.List(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("queued = %d, want none from a malformed answer", len(rows))
	}
}

func TestReviewNoopWhenTurnHasNoText(t *testing.T) {
	o, _, _, _ := newObserver(t, enabledCfg())
	calls := 0
	o.generateFn = func(context.Context, promptInput) (inference.GenerateResponse, error) {
		calls++
		return answer(`{"memory":[]}`), nil
	}
	res := &agent.Result{Status: agent.StatusCompleted, Messages: []message.Message{
		{Role: message.RoleTool, Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: message.NewTextContent("ok"),
			}},
		}}},
	}}
	if err := o.review(context.Background(), agent.Identity{ConversationID: "s-1"}, res, 1); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("a text-free turn ran %d model calls, want none", calls)
	}
}

// TestReportUsageAttributesTheParentConversation pins the cross-cutting
// rule: a background review call is recorded against the conversation
// it reviewed, both in the usage observer and the session totals.
func TestReportUsageAttributesTheParentConversation(t *testing.T) {
	o, _, _, usage := newObserver(t, enabledCfg())
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	o.sessions = store

	o.reportUsage(context.Background(), agent.Identity{ConversationID: "s-1"},
		inference.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})

	if usage.count() != 1 {
		t.Fatalf("usage observer reports = %d, want 1", usage.count())
	}
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, meta := range metas {
		if meta.ID == "s-1" {
			found = true
			if meta.Usage.TotalTokens != 15 {
				t.Fatalf("session total = %d, want 15", meta.Usage.TotalTokens)
			}
		}
	}
	if !found {
		t.Fatalf("conversation s-1 missing from %+v", metas)
	}
}

func TestSuggestionIDIsStable(t *testing.T) {
	if got := suggestionID(agent.Identity{RunID: "run-1"}, 2); got != "rv-run-1-2" {
		t.Fatalf("id = %q, want rv-run-1-2", got)
	}
	if got := suggestionID(agent.Identity{ConversationID: "s-1"}, 0); got != "rv-s-1-0" {
		t.Fatalf("id = %q, want the conversation fallback rv-s-1-0", got)
	}
}

// TestFactoryMarksOptionalDepsOptional pins the assembly contract: only
// the queue is required, so a runtime without a user database, a router
// or a skills registry still assembles the hook.
func TestFactoryMarksOptionalDepsOptional(t *testing.T) {
	required := map[string]bool{}
	for _, dep := range (Factory{}).Spec().Deps {
		required[dep.Name] = dep.Required
	}
	if !required["queue"] {
		t.Fatal("the queue dependency must be required")
	}
	for _, name := range []string{"memory", "skills", "sessions", "router", "observer"} {
		if required[name] {
			t.Fatalf("dependency %q must be optional", name)
		}
	}
}

func TestFactoryNewWiresOptionalDeps(t *testing.T) {
	ctx := context.Background()
	memBinding := &userstore.Binding{
		Memory: newMemoryStore(t),
		Config: config.UserMemoryConfig{Enabled: true, InjectMaxItems: 12, InjectMaxChars: 2048},
	}
	observer := opmemory.UsageObserverFunc(func(context.Context, inference.Usage) {})
	value, err := (Factory{}).New(ctx, resource.Input{
		Settings: []byte(`{"work_dir":"/w","max_output_tokens":512}`),
		Deps: map[string]any{
			"queue":    &reviewstore.Binding{Queue: newQueueStore(t), Config: enabledCfg()},
			"memory":   memBinding,
			"router":   newRouter(t),
			"observer": observer,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	o, ok := value.(*Observer)
	if !ok {
		t.Fatalf("value = %T, want *Observer", value)
	}
	if o.memory != memBinding {
		t.Fatal("memory binding not wired")
	}
	if o.router == nil {
		t.Fatal("router not wired")
	}
	if o.usage == nil {
		t.Fatal("usage observer not wired")
	}
	if o.settings.WorkDir != "/w" || o.settings.MaxOutputTokens != 512 {
		t.Fatalf("settings = %+v", o.settings)
	}

	// A queue-only assembly still builds: every other dep is optional.
	minimal, err := (Factory{}).New(ctx, resource.Input{
		Settings: []byte(`{}`),
		Deps:     map[string]any{"queue": &reviewstore.Binding{Queue: newQueueStore(t), Config: enabledCfg()}},
	})
	if err != nil {
		t.Fatalf("New(queue only): %v", err)
	}
	mo := minimal.(*Observer)
	if mo.memory != nil || mo.router != nil || mo.usage != nil || mo.skills != nil || mo.sessions != nil {
		t.Fatalf("queue-only assembly wired unexpected deps: %+v", mo)
	}
}

// TestDeployWiresReviewHook pins the deploy wiring the runtime depends
// on: the review binding resource exists with the host-attached queue
// store, the agents.yaml observe hook is declared (it is the only thing
// that ever calls OnRunEnd), it lists all six of its dependencies, and
// the memory dependency stays optional so a runtime without a user
// database still assembles.
func TestDeployWiresReviewHook(t *testing.T) {
	ctx := context.Background()
	mgr, err := config.Open(config.Options{UserDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}

	res, ok := view.Document.Resources["review"]
	if !ok {
		t.Fatal("the review binding resource is not declared")
	}
	if res.Kind != reviewstore.ResourceKind || res.Impl != reviewstore.ResourceImpl {
		t.Fatalf("review resource = %s/%s, want %s/%s",
			res.Kind, res.Impl, reviewstore.ResourceKind, reviewstore.ResourceImpl)
	}
	if res.Deps["store"] != "review.queue" {
		t.Fatalf("review deps = %+v, want it bound to the review.queue store", res.Deps)
	}

	memory, ok := view.Document.Resources["usermemory"]
	if !ok {
		t.Fatal("the usermemory binding resource is not declared")
	}
	if memory.Kind != userstore.ResourceKind {
		t.Fatalf("usermemory kind = %q, want %q", memory.Kind, userstore.ResourceKind)
	}
	if memory.Deps["store"] != "user.memory" {
		t.Fatalf("usermemory deps = %+v, want it bound to the user.memory store", memory.Deps)
	}

	// The observe hook itself lives in agents.yaml, which is merged as
	// its own layer and is not reachable through Document.Resources.
	type hookSpec struct {
		Type     string            `yaml:"type"`
		Deps     map[string]string `yaml:"deps"`
		Settings map[string]any    `yaml:"settings"`
	}
	var agents struct {
		Agents map[string]struct {
			Observe []hookSpec `yaml:"observe"`
		} `yaml:"agents"`
	}
	data, err := config.FS().ReadFile("assets/agents.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yamlv4.Unmarshal(data, &agents); err != nil {
		t.Fatalf("parse agents.yaml: %v", err)
	}
	var hook *hookSpec
	for i := range agents.Agents["assistant"].Observe {
		if agents.Agents["assistant"].Observe[i].Type == ResourceImpl {
			hook = &agents.Agents["assistant"].Observe[i]
		}
	}
	if hook == nil {
		t.Fatal("agents.yaml declares no opencraft.review observe hook")
	}
	want := map[string]string{
		"queue":    "review",
		"memory":   "usermemory",
		"skills":   "skills",
		"sessions": "sessions",
		"router":   "router",
		"observer": "observer",
	}
	if len(hook.Deps) != len(want) {
		t.Fatalf("review hook deps = %+v, want %v", hook.Deps, want)
	}
	for name, ref := range want {
		if hook.Deps[name] != ref {
			t.Fatalf("review hook dep %s = %q, want %q", name, hook.Deps[name], ref)
		}
	}
	// The hook files workspace-scoped facts under the workspace its
	// turn ran in: without the resolved work_dir the store rejects
	// every workspace candidate and the review silently degrades to
	// global facts (and never sees the workspace's existing ones).
	if got := hook.Settings["work_dir"]; got != "${ocraft:WORKDIR}" {
		t.Fatalf("review hook settings.work_dir = %v, want ${ocraft:WORKDIR}", got)
	}

	// The stores the two bindings are bound to are the host-attached
	// external dependencies (runtime.yaml), not resources the deploy
	// rebuilds per generation: that is what keeps an accepted
	// suggestion and a runtime reload writing to the same user.db.
	var runtimeDoc struct {
		Runtime struct {
			ExternalDeps []struct {
				Name     string `yaml:"name"`
				Contract string `yaml:"contract"`
			} `yaml:"external_deps"`
		} `yaml:"runtime"`
	}
	runtimeData, err := config.FS().ReadFile("assets/runtime.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yamlv4.Unmarshal(runtimeData, &runtimeDoc); err != nil {
		t.Fatalf("parse runtime.yaml: %v", err)
	}
	contracts := map[string]string{}
	for _, dep := range runtimeDoc.Runtime.ExternalDeps {
		contracts[dep.Name] = dep.Contract
	}
	if contracts["review.queue"] != reviewstore.StoreContract {
		t.Fatalf("review.queue contract = %q, want %q",
			contracts["review.queue"], reviewstore.StoreContract)
	}
	if contracts["user.memory"] != userstore.StoreContract {
		t.Fatalf("user.memory contract = %q, want %q",
			contracts["user.memory"], userstore.StoreContract)
	}

	// memory is declared above but must stay optional: the whole point
	// of the empty-memory binding is that the hook assembles anyway.
	for _, dep := range (Factory{}).Spec().Deps {
		if dep.Name == "memory" && dep.Required {
			t.Fatal("the memory dep must stay optional (user-db-less runtimes)")
		}
	}
}
