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

// TestContextUsageNormalizeClampsAnOvershootingEstimate pins the other end:
// a provider that counts differently, or a prompt whose sections duplicate
// what the transcript already holds, can make the estimates exceed the
// measurement. The meter must not publish a negative slice for that.
func TestContextUsageNormalizeClampsAnOvershootingEstimate(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 1000,
		Categories:     []ContextCategory{{ID: ContextCategoryInstructions, Tokens: 4000}},
	}
	usage.Normalize()

	if got := usage.Token(ContextCategoryTools); got != 0 {
		t.Fatalf("residual = %d, want 0 when the estimates overshoot", got)
	}
	if got := usage.TotalTokens(); got != 4000 {
		t.Fatalf("total = %d, want the estimates' sum when nothing is left over", got)
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
