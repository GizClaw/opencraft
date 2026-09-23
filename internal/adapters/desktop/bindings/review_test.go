package bindings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// reviewBinding builds the review service over a private temp root.
func reviewBinding(t *testing.T, workDir string) *Review {
	t.Helper()
	dir := t.TempDir()
	return NewReviewBinding(core.NewCore(dir, dir, workDir))
}

// seedSuggestion queues one memory suggestion the way the review hook
// does, so the accept path is exercised against a real row.
func seedSuggestion(
	t *testing.T, b *Review, id, text, scope, workspace string,
) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"text":   text,
		"scope":  scope,
		"kind":   "convention",
		"reason": "stays true",
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := b.core.Runtime.Manager().ReviewStore()
	if queue == nil {
		t.Fatal("review store is not attached")
	}
	if _, _, err := queue.Create(context.Background(), reviewstore.Suggestion{
		ID:                 id,
		Kind:               reviewstore.KindMemory,
		Payload:            payload,
		Reason:             "stays true",
		SourceWorkspace:    workspace,
		SourceConversation: "conv-1",
		SourceRun:          "run-1",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewSettingsDefaultOff(t *testing.T) {
	b := reviewBinding(t, "")
	state, err := b.ReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatal("the review must ship disabled")
	}
	if state.EveryTurns != config.ReviewDefaultEveryTurns ||
		state.MinToolCalls != config.ReviewDefaultMinToolCalls ||
		state.MaxSuggestions != config.ReviewDefaultMaxSuggestions ||
		state.TimeoutSeconds != config.ReviewDefaultTimeoutSeconds {
		t.Fatalf("state = %+v", state)
	}
	if !state.OnFailure {
		t.Fatalf("on_failure default = %v", state.OnFailure)
	}
	if state.MinEveryTurns != config.ReviewMinEveryTurns ||
		state.MaxEveryTurns != config.ReviewMaxEveryTurns ||
		state.MinMaxSuggestions != config.ReviewMinMaxSuggestions ||
		state.MaxMaxSuggestions != config.ReviewMaxMaxSuggestions ||
		state.MinTimeoutSeconds != config.ReviewMinTimeoutSeconds ||
		state.MaxTimeoutSeconds != config.ReviewMaxTimeoutSeconds {
		t.Fatalf("bounds = %+v", state)
	}
	if state.Available {
		t.Fatal("available without a user database")
	}
	if state.Pending != 0 || state.Accepted != 0 || state.Discarded != 0 {
		t.Fatalf("counts = %+v", state)
	}
}

func TestSaveReviewSettingsRoundTrip(t *testing.T) {
	b := reviewBinding(t, "")
	if err := b.SaveReviewSettings(ReviewSettingsRequest{
		Enabled:      true,
		EveryTurns:   10,
		MinToolCalls: 2,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadReview(b.core.UserDir)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := loaded.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !effective.Enabled || effective.EveryTurns != 10 ||
		effective.MinToolCalls != 2 {
		t.Fatalf("effective = %+v", effective)
	}
	state, err := b.ReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.EveryTurns != 10 || state.MinToolCalls != 2 {
		t.Fatalf("state = %+v", state)
	}
	// The knobs the page cannot edit keep the values the document has.
	if state.MaxSuggestions != config.ReviewDefaultMaxSuggestions ||
		state.TimeoutSeconds != config.ReviewDefaultTimeoutSeconds ||
		!state.OnFailure {
		t.Fatalf("state = %+v", state)
	}

	if err := b.SaveReviewSettings(ReviewSettingsRequest{
		Enabled:      true,
		EveryTurns:   config.ReviewMaxEveryTurns + 1,
		MinToolCalls: 2,
	}); err == nil {
		t.Fatal("an out-of-range cadence must be refused")
	}
	state, err = b.ReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	if state.EveryTurns != 10 {
		t.Fatalf("failed save changed the stored settings: %+v", state)
	}
}

func TestReviewSuggestionsWithoutUserDB(t *testing.T) {
	b := reviewBinding(t, "")
	rows, err := b.ReviewSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want empty", rows)
	}
	_, err = b.AcceptReviewSuggestion("rs-1")
	wantErrorContaining(t, err, "no user database")
	_, err = b.DiscardReviewSuggestion("rs-1")
	wantErrorContaining(t, err, "no user database")
}

func TestReviewSuggestionsCarryTheirCandidate(t *testing.T) {
	b := reviewBinding(t, "")
	openUserDBOn(t, b.core)
	seedSuggestion(t, b, "rs-1", "The repo uses tabs", "global", "")

	rows, err := b.ReviewSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.ID != "rs-1" || row.Status != reviewstore.StatusPending {
		t.Fatalf("row = %+v", row)
	}
	if row.Kind != reviewstore.KindMemory {
		t.Fatalf("kind = %q", row.Kind)
	}
	// The payload is decoded for the page and kept verbatim beside it.
	if row.Text != "The repo uses tabs" || row.Scope != "global" ||
		row.CandidateKind != "convention" {
		t.Fatalf("candidate = %+v", row)
	}
	if row.Payload == "" || row.Reason == "" {
		t.Fatalf("row = %+v", row)
	}
	if row.SourceConversation != "conv-1" || row.SourceRun != "run-1" {
		t.Fatalf("provenance = %+v", row)
	}
	if row.CreatedAt == "" {
		t.Fatalf("created_at = %q", row.CreatedAt)
	}
}

func TestAcceptReviewSuggestionWritesMemory(t *testing.T) {
	b := reviewBinding(t, "")
	openUserDBOn(t, b.core)
	seedSuggestion(t, b, "rs-1", "The repo uses tabs", "global", "")

	out, err := b.AcceptReviewSuggestion("rs-1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Suggestion.Status != reviewstore.StatusAccepted {
		t.Fatalf("suggestion = %+v", out.Suggestion)
	}
	if out.Fact.Text != "The repo uses tabs" ||
		out.Fact.Scope != userstore.ScopeGlobal {
		t.Fatalf("fact = %+v", out.Fact)
	}
	// The fact landed in the store the memory card reads, through the
	// same write path the remember tool uses.
	facts, err := b.core.Runtime.Manager().MemoryStore().List(
		context.Background(), userstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Text != "The repo uses tabs" {
		t.Fatalf("facts = %+v", facts)
	}
	// Accepted rows leave the pending list and cannot be accepted twice.
	rows, err := b.ReviewSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("pending = %+v", rows)
	}
	_, err = b.AcceptReviewSuggestion("rs-1")
	wantErrorContaining(t, err, "already accepted")
}

func TestAcceptReviewSuggestionUsesItsOwnWorkspace(t *testing.T) {
	workspace := t.TempDir()
	// The window has moved to another workspace; the suggestion still
	// carries the one its turn ran in.
	b := reviewBinding(t, t.TempDir())
	openUserDBOn(t, b.core)
	seedSuggestion(t, b, "rs-1", "The repo uses tabs", "workspace", workspace)

	out, err := b.AcceptReviewSuggestion("rs-1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Fact.Scope != userstore.ScopeWorkspace ||
		out.Fact.Workspace != workspace {
		t.Fatalf("fact = %+v", out.Fact)
	}
}

func TestAcceptReviewSuggestionRejectsUndecodablePayload(t *testing.T) {
	b := reviewBinding(t, "")
	openUserDBOn(t, b.core)
	queue := b.core.Runtime.Manager().ReviewStore()
	if _, _, err := queue.Create(context.Background(), reviewstore.Suggestion{
		ID:      "rs-bad",
		Kind:    reviewstore.KindMemory,
		Payload: json.RawMessage(`{"text":123}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AcceptReviewSuggestion("rs-bad"); err == nil {
		t.Fatal("a payload the store cannot read must not be accepted")
	}
	// The row stays pending: a refused accept must not be recorded as
	// applied.
	suggestion, err := queue.Get(context.Background(), "rs-bad")
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.Status != reviewstore.StatusPending {
		t.Fatalf("status = %q", suggestion.Status)
	}
}

func TestDiscardReviewSuggestionWritesNothing(t *testing.T) {
	b := reviewBinding(t, "")
	openUserDBOn(t, b.core)
	seedSuggestion(t, b, "rs-1", "The repo uses tabs", "global", "")

	row, err := b.DiscardReviewSuggestion("rs-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != reviewstore.StatusDiscarded {
		t.Fatalf("row = %+v", row)
	}
	facts, err := b.core.Runtime.Manager().MemoryStore().List(
		context.Background(), userstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("discarding wrote memory: %+v", facts)
	}
	state, err := b.ReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != 0 || state.Discarded != 1 {
		t.Fatalf("counts = %+v", state)
	}
}
