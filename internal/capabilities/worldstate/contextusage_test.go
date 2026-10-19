package worldstate

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// TestEstimateContextUsageSplitsSections pins the classification the meter
// draws: every section the harness renders lands in the category that names
// what it is, the tail block is its own slice, and the request text is
// prompt the conversation paid for.
func TestEstimateContextUsageSplitsSections(t *testing.T) {
	env := newTextSection("environment", message.RoleUser, "workspace: /tmp/w")
	docs := newTextSection("agents_md", message.RoleUser, "run go test ./...")
	memory := newTextSection("user_memory", message.RoleUser, "prefers Chinese")
	summary := newTextSection("memory_summary", message.RoleAssistant, "earlier: shipped the meter")
	raw := newTextSection("memory_raw", message.RoleUser, "and then we did this")
	base := newTextSection("base_identity", message.RoleSystem, "you are opencraft")
	mode := newTextSection("mode_plan", message.RoleSystem, "plan first")
	// An id the meter does not draw: its tokens belong to the residual,
	// not to a category of their own.
	unknown := newTextSection("something_new", message.RoleUser, "surprise")

	sections := []Section{
		base, mode, env, docs, memory, summary, raw, unknown,
	}
	est := estimateContextUsage(sections, "plan: 1. ship", 5000, 120)

	want := map[string]int{
		ocsessions.ContextCategoryInstructions: summarytext.EstimateTokens(
			[]message.Message{base.Message, mode.Message},
		),
		ocsessions.ContextCategoryEnvironment: summarytext.EstimateTokens(
			[]message.Message{env.Message},
		),
		ocsessions.ContextCategoryProjectDocs: summarytext.EstimateTokens(
			[]message.Message{docs.Message},
		),
		ocsessions.ContextCategoryUserMemory: summarytext.EstimateTokens(
			[]message.Message{memory.Message},
		),
		ocsessions.ContextCategoryMemorySummary: summarytext.EstimateTokens(
			[]message.Message{summary.Message},
		),
		ocsessions.ContextCategoryConversation: summarytext.EstimateTokens(
			[]message.Message{raw.Message},
		) + 5000 + 120,
		ocsessions.ContextCategoryTurnContext: summarytext.EstimateText("plan: 1. ship"),
	}
	for id, tokens := range want {
		if got := est.Categories[id]; got != tokens {
			t.Errorf("%s = %d, want %d", id, got, tokens)
		}
	}
	// The unknown section and anything else unclassified may not appear
	// under a category the meter would draw as if it knew what it was.
	if got := est.Categories["something_new"]; got != 0 {
		t.Fatalf("an unrecognised section id became a category (%d tokens)", got)
	}
	// The rendered order is the canonical one, so a consumer that walks
	// the list does not have to sort.
	var order []string
	for _, c := range est.categories() {
		order = append(order, c.ID)
	}
	for i := 1; i < len(order); i++ {
		rank := func(id string) int {
			for j, want := range ocsessions.ContextCategories {
				if want == id {
					return j
				}
			}
			return -1
		}
		if rank(order[i-1]) > rank(order[i]) {
			t.Fatalf("categories out of order: %v", order)
		}
	}
}

// TestEstimateContextUsageEmptyPromptDeletesTheVar pins the reused-board
// guard: a turn that rendered nothing must not leave the previous turn's
// estimates on the board to be paired with this turn's measurement.
func TestEstimateContextUsageEmptyPromptDeletesTheVar(t *testing.T) {
	board := agent.NewBoard()
	board.SetVar(config.BoardVarContextUsage, `{"categories":{"instructions":900}}`)

	estimateContextUsage(nil, "", 0, 0).writeTo(board)

	if _, ok := board.GetVar(config.BoardVarContextUsage); ok {
		t.Fatal("stale context estimate survived a turn that rendered nothing")
	}
}

// TestContextUsageFromBoardPairsEstimateWithMeasurement pins the turn-end
// join: the provider's own prompt count is the total, the estimates fill
// the slices it can account for, and what is left is the residual.
func TestContextUsageFromBoardPairsEstimateWithMeasurement(t *testing.T) {
	board := agent.NewBoard()
	estimateContextUsage(
		[]Section{newTextSection("environment", message.RoleUser, "workspace: /tmp/w")},
		"",
		0,
		0,
	).writeTo(board)
	board.SetVar(config.BoardVarLLMUsage, inference.Usage{
		InputTokens: 40000,
		Model: model.ModelRef{ID: model.ModelID{
			Provider: "openai", Name: "gpt-6-astra",
		}},
	})

	usage, ok := ContextUsageFromBoard(board)
	if !ok {
		t.Fatal("a turn that prepared and called the model produced no snapshot")
	}
	if usage.MeasuredTokens != 40000 {
		t.Fatalf("measured = %d, want the provider's 40000", usage.MeasuredTokens)
	}
	if usage.Model != "gpt-6-astra" {
		t.Fatalf("model = %q, want the serving model", usage.Model)
	}
	if got := usage.TotalTokens(); got != 40000 {
		t.Fatalf("total = %d, want the measurement (the bar must fill exactly)", got)
	}
	if got := usage.Token(ocsessions.ContextCategoryTools); got != 40000-usage.EstimatedTokens() {
		t.Fatalf("residual = %d, want measured minus estimates", got)
	}
	if got := usage.Token(ocsessions.ContextCategoryEnvironment); got == 0 {
		t.Fatal("the environment estimate vanished when it was paired with usage")
	}
}

// TestContextUsageFromBoardWithoutUsage pins the estimate-only shape: a turn
// whose provider reported no tokens still describes its prompt, and the
// snapshot says so by leaving the measurement at zero.
func TestContextUsageFromBoardWithoutUsage(t *testing.T) {
	board := agent.NewBoard()
	estimateContextUsage(
		[]Section{newTextSection("agents_md", message.RoleUser, "run go test ./...")},
		"",
		0,
		0,
	).writeTo(board)

	usage, ok := ContextUsageFromBoard(board)
	if !ok {
		t.Fatal("an estimated prompt is still a snapshot")
	}
	if usage.MeasuredTokens != 0 {
		t.Fatalf("measured = %d, want 0 without a usage report", usage.MeasuredTokens)
	}
	if usage.Token(ocsessions.ContextCategoryTools) != 0 {
		t.Fatal("no measurement means no residual to subtract")
	}
}

// TestContextUsageFromBoardSkipsUnpreparedTurns pins the guard the hook
// relies on: a turn that never rendered a prompt (a custom graph, a turn
// that failed before its first call) has nothing to describe.
func TestContextUsageFromBoardSkipsUnpreparedTurns(t *testing.T) {
	if _, ok := ContextUsageFromBoard(nil); ok {
		t.Fatal("nil board produced a snapshot")
	}
	if _, ok := ContextUsageFromBoard(agent.NewBoard()); ok {
		t.Fatal("a board with no estimate produced a snapshot")
	}
	broken := agent.NewBoard()
	broken.SetVar(config.BoardVarContextUsage, "{not json")
	if _, ok := ContextUsageFromBoard(broken); ok {
		t.Fatal("an unreadable estimate produced a snapshot")
	}
}

// TestRecordContextUsagePersistsAtTurnEnd pins the turn-end half: the
// snapshot reaches the conversation's store, and a sessionless run (a
// delegated "ctx-" run) writes nothing.
func TestRecordContextUsagePersistsAtTurnEnd(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-context-1"

	board := agent.NewBoard()
	estimateContextUsage(
		[]Section{newTextSection("base_identity", message.RoleSystem, "you are opencraft")},
		"",
		0,
		0,
	).writeTo(board)

	ctx := context.Background()
	recordContextUsage(ctx, store, "ctx-9ae1", board)
	if _, err := store.ReadContextUsage("ctx-9ae1"); err == nil {
		t.Fatal("a sessionless run wrote a context snapshot")
	}

	recordContextUsage(ctx, store, contextID, board)
	usage, err := store.ReadContextUsage(contextID)
	if err != nil {
		t.Fatalf("ReadContextUsage: %v", err)
	}
	if usage.Token(ocsessions.ContextCategoryInstructions) == 0 {
		t.Fatalf("stored snapshot lost its estimates: %+v", usage)
	}
	if usage.At.IsZero() {
		t.Fatal("stored snapshot has no timestamp")
	}

	// A turn that produced no estimate leaves the stored one alone:
	// yesterday's numbers describe the conversation better than a blank
	// meter does.
	recordContextUsage(ctx, store, contextID, agent.NewBoard())
	again, err := store.ReadContextUsage(contextID)
	if err != nil {
		t.Fatalf("ReadContextUsage: %v", err)
	}
	if again.TotalTokens() != usage.TotalTokens() {
		t.Fatalf("a turn with no estimate overwrote the stored snapshot: %+v", again)
	}
}
