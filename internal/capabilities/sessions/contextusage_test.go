package sessions

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestContextUsageNormalizePutsTheLeftoverInTheResidual pins the shape the
// meter relies on: the categories sum to the provider's own count, and the
// difference between that count and the harness's estimates is one bucket
// rather than noise spread over slices that would then read as measurement.
func TestContextUsageNormalizePutsTheLeftoverInTheResidual(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 50000,
		Categories: []ContextCategory{
			{ID: ContextCategoryConversation, Tokens: 20000},
			{ID: ContextCategoryInstructions, Tokens: 4000},
			// Well above any estimate: the residual is what is left
			// after subtracting, so nothing is invented from nothing.
			{ID: ContextCategoryTools, Tokens: 1},
		},
	}
	usage.Normalize()

	if got := usage.TotalTokens(); got != 50000 {
		t.Fatalf("total = %d, want the measured 50000", got)
	}
	if got := usage.Token(ContextCategoryTools); got != 26000 {
		t.Fatalf("residual = %d, want 26000 (measured - estimates)", got)
	}
	if got := usage.EstimatedTokens(); got != 24000 {
		t.Fatalf("estimated = %d, want 24000", got)
	}
	// Canonical order, not insertion order: the meter stacks the bar by
	// walking this list.
	want := []string{
		ContextCategoryInstructions,
		ContextCategoryConversation,
		ContextCategoryTools,
	}
	for i, id := range want {
		if usage.Categories[i].ID != id {
			t.Fatalf("categories[%d] = %q, want %q (order = %+v)",
				i, usage.Categories[i].ID, id, usage.Categories)
		}
	}
}

// TestContextUsageNormalizeScalesAnOvershootingEstimate pins the other end:
// the turn that folds the conversation mid-way estimated the transcript it
// opened with and then sent a fraction of it, so the estimates run past the
// measurement that settles them. The meter divides its bar by the slices'
// sum, so leaving them at their own size would draw a prompt bigger than
// the window that served it; shrinking them keeps the total honest and
// keeps each category's share.
func TestContextUsageNormalizeScalesAnOvershootingEstimate(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 1000,
		Categories: []ContextCategory{
			{ID: ContextCategoryConversation, Tokens: 4000},
			{ID: ContextCategoryInstructions, Tokens: 1000},
			// A stale residual from an earlier generation is recomputed,
			// never scaled: it is derived, not estimated.
			{ID: ContextCategoryTools, Tokens: 500},
		},
	}
	usage.Normalize()

	if got := usage.TotalTokens(); got != 1000 {
		t.Fatalf("total = %d, want the measured 1000", got)
	}
	if got := usage.Token(ContextCategoryConversation); got != 800 {
		t.Fatalf("conversation = %d, want 800 (four fifths of the measurement)", got)
	}
	if got := usage.Token(ContextCategoryInstructions); got != 200 {
		t.Fatalf("instructions = %d, want 200 (one fifth)", got)
	}
	if got := usage.Token(ContextCategoryTools); got != 0 {
		t.Fatalf("residual = %d, want 0 when the estimates overshoot", got)
	}
	if !usage.Scaled {
		t.Fatal("a scaled split was not marked, so the UI cannot say it is one")
	}
}

// TestContextUsageNormalizeScalesByLargestRemainder pins the rounding: the
// scaled slices add up to the measurement exactly, and the tokens that
// flooring drops go to the categories whose fractional parts came closest
// to a whole — not to whichever id map iteration happened to visit last.
func TestContextUsageNormalizeScalesByLargestRemainder(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 37,
		Categories: []ContextCategory{
			{ID: ContextCategoryConversation, Tokens: 58},
			{ID: ContextCategoryInstructions, Tokens: 29},
			{ID: ContextCategoryTurnContext, Tokens: 13},
		},
	}
	usage.Normalize()

	// 58/100, 29/100 and 13/100 of 37 are 21.46, 10.73 and 4.81: floors
	// 21/10/4 leave two tokens, which go to turn_context (.81) and
	// instructions (.73).
	want := map[string]int{
		ContextCategoryConversation: 21,
		ContextCategoryInstructions: 11,
		ContextCategoryTurnContext:  5,
	}
	for id, tokens := range want {
		if got := usage.Token(id); got != tokens {
			t.Fatalf("%s = %d, want %d (categories = %+v)", id, got, tokens, usage.Categories)
		}
	}
	if got := usage.TotalTokens(); got != 37 {
		t.Fatalf("total = %d, want the measured 37 exactly", got)
	}
}

// TestContextUsageRoundTrip pins persistence: the snapshot survives a read
// and a document that was never written reads as "nothing measured yet",
// which is the state the composer draws as no meter at all.
func TestContextUsageRoundTrip(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.ReadContextUsage(id); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unmetered conversation read = %v, want os.ErrNotExist", err)
	}

	at := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	want := ContextUsage{
		MeasuredTokens: 12345,
		Model:          "gpt-6-astra",
		At:             at,
		Categories: []ContextCategory{
			{ID: ContextCategoryInstructions, Tokens: 2000},
			{ID: ContextCategoryConversation, Tokens: 8000},
			{ID: ContextCategoryTools, Tokens: 2345},
		},
	}
	if err := store.WriteContextUsage(id, want); err != nil {
		t.Fatalf("WriteContextUsage: %v", err)
	}
	got, err := store.ReadContextUsage(id)
	if err != nil {
		t.Fatalf("ReadContextUsage: %v", err)
	}
	if got.MeasuredTokens != want.MeasuredTokens || got.Model != want.Model ||
		!got.At.Equal(at) || len(got.Categories) != len(want.Categories) {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
	if got.Token(ContextCategoryTools) != 2345 {
		t.Fatalf("categories lost their values: %+v", got.Categories)
	}
}

// TestReadContextUsageRepairsAnOvershootingDocument pins the read-side
// repair: a document stored under the old rule (the turn that folded, whose
// estimates were a generation old) holds slices that sum past its own
// measurement. Without normalizing on read, this conversation would keep
// drawing a prompt larger than the window until someone ran another turn on
// it.
func TestReadContextUsageRepairsAnOvershootingDocument(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := ContextUsage{
		MeasuredTokens: 89772,
		Categories: []ContextCategory{
			{ID: ContextCategoryInstructions, Tokens: 2437},
			{ID: ContextCategoryConversation, Tokens: 582626},
		},
	}
	if err := store.WriteState(id, DocumentContextUsage, stale); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	got, err := store.ReadContextUsage(id)
	if err != nil {
		t.Fatalf("ReadContextUsage: %v", err)
	}
	if got.TotalTokens() != 89772 {
		t.Fatalf("total = %d, want the stored measurement 89772", got.TotalTokens())
	}
	if !got.Scaled {
		t.Fatal("the repaired snapshot was not marked as scaled")
	}
	// The shares survive the shrink: conversation was 99.6% of the
	// estimates and stays the bigger slice.
	if conv, instr := got.Token(ContextCategoryConversation), got.Token(ContextCategoryInstructions); !(conv > instr && instr > 0) {
		t.Fatalf("conversation = %d, instructions = %d: shares lost (%+v)", conv, instr, got.Categories)
	}
}

// TestWriteContextUsageDropsEmptySnapshots pins the guard that keeps a turn
// which never reached the model from erasing the previous turn's numbers:
// there is nothing to show in a zero snapshot, and something useful already
// stored.
func TestWriteContextUsageDropsEmptySnapshots(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.WriteContextUsage(id, ContextUsage{
		Categories: []ContextCategory{{ID: ContextCategoryInstructions, Tokens: 100}},
	}); err != nil {
		t.Fatalf("WriteContextUsage: %v", err)
	}
	if err := store.WriteContextUsage(id, ContextUsage{Model: "x"}); err != nil {
		t.Fatalf("WriteContextUsage(empty): %v", err)
	}
	got, err := store.ReadContextUsage(id)
	if err != nil {
		t.Fatalf("ReadContextUsage: %v", err)
	}
	if got.Token(ContextCategoryInstructions) != 100 {
		t.Fatalf("an empty snapshot overwrote the stored one: %+v", got)
	}
}
