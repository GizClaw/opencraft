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
	if got := usage.Token(ContextCategoryConversation); got != 20000 {
		t.Fatalf("conversation = %d, want its own estimate 20000", got)
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

// TestContextUsageNormalizeCapsTheConversationAtTheMeasurement pins the
// fold: the turn estimated the transcript it opened with and then sent a
// fraction of it, so the conversation estimate runs past the measurement
// that settles it. The meter divides its bar by the slices' sum, so that
// estimate cannot be drawn; but shrinking every slice to fit - which is
// what proportionally scaling the lot did - also cut the rows the harness
// rendered, drawing a large instruction layer as a handful of tokens
// because a transcript estimate had to be absorbed somewhere. The
// conversation row takes the hit alone: it is the row the fold rewrote.
func TestContextUsageNormalizeCapsTheConversationAtTheMeasurement(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 3000,
		Categories: []ContextCategory{
			{ID: ContextCategoryConversation, Tokens: 400000},
			{ID: ContextCategoryInstructions, Tokens: 1000},
			// A stale residual from an earlier generation is recomputed,
			// never scaled: it is derived, not estimated.
			{ID: ContextCategoryTools, Tokens: 500},
		},
	}
	usage.Normalize()

	if got := usage.TotalTokens(); got != 3000 {
		t.Fatalf("total = %d, want the measured 3000", got)
	}
	if got := usage.Token(ContextCategoryConversation); got != 2000 {
		t.Fatalf("conversation = %d, want 2000 (what the measurement left)", got)
	}
	if got := usage.Token(ContextCategoryInstructions); got != 1000 {
		t.Fatalf("instructions = %d, want its own estimate 1000, not a share", got)
	}
	if got := usage.Token(ContextCategoryTools); got != 0 {
		t.Fatalf("residual = %d, want 0 when the conversation filled the room", got)
	}
	if !usage.ConversationCapped {
		t.Fatal("a capped conversation row was not marked, so the UI cannot say it is a remainder")
	}
	// A stored snapshot is normalized again on read, so the rule has to be
	// a fixed point: the capped row must not shrink a second time.
	again := usage
	again.Categories = append([]ContextCategory(nil), usage.Categories...)
	again.Normalize()
	if len(again.Categories) != len(usage.Categories) {
		t.Fatalf("second normalize changed the rows: %+v -> %+v", usage.Categories, again.Categories)
	}
	for i := range usage.Categories {
		if again.Categories[i] != usage.Categories[i] {
			t.Fatalf("second normalize changed a row: %+v -> %+v", usage.Categories, again.Categories)
		}
	}
	if !again.ConversationCapped {
		t.Fatal("second normalize dropped the remainder marker")
	}
}

// TestContextUsageNormalizeKeepsTheConversationEstimateItCanTrust pins the
// other half of the rule: when the estimate fits what the measurement left,
// it is the size of the row and the leftover is the residual - the shape
// every turn without a fold has, and the one that keeps the tools row
// meaningful.
func TestContextUsageNormalizeKeepsTheConversationEstimateItCanTrust(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 30000,
		Categories: []ContextCategory{
			{ID: ContextCategoryConversation, Tokens: 20000},
			{ID: ContextCategoryInstructions, Tokens: 3000},
		},
	}
	usage.Normalize()

	if got := usage.Token(ContextCategoryConversation); got != 20000 {
		t.Fatalf("conversation = %d, want its estimate 20000", got)
	}
	if got := usage.Token(ContextCategoryInstructions); got != 3000 {
		t.Fatalf("instructions = %d, want 3000", got)
	}
	if got := usage.Token(ContextCategoryTools); got != 7000 {
		t.Fatalf("residual = %d, want 7000 (measured - estimates)", got)
	}
	if usage.ConversationCapped {
		t.Fatal("a conversation that fits the measurement was marked as capped")
	}
}

// TestContextUsageNormalizeScalesHarnessRowsThatClaimTheWholePrompt pins the
// remaining case: a short turn whose rendered rows alone reach the
// measurement. There the harness's own estimate is the one that is wrong,
// the miss is a few percent wide rather than orders of magnitude, and the
// only way to keep the bar inside the window is to scale them - the
// conversation included, so the user's own message stays visible.
func TestContextUsageNormalizeScalesHarnessRowsThatClaimTheWholePrompt(t *testing.T) {
	usage := ContextUsage{
		MeasuredTokens: 3500,
		Categories: []ContextCategory{
			{ID: ContextCategoryConversation, Tokens: 50},
			{ID: ContextCategoryInstructions, Tokens: 2400},
			{ID: ContextCategoryEnvironment, Tokens: 1500},
		},
	}
	usage.Normalize()

	if got := usage.TotalTokens(); got != 3500 {
		t.Fatalf("total = %d, want the measured 3500", got)
	}
	if got := usage.Token(ContextCategoryConversation); got == 0 {
		t.Fatal("the user's own message vanished from a scaled split")
	}
	// 2400 of 3950 estimated tokens, scaled onto 3500, is 2126.6: the
	// floor and the token largest-remainder hands back.
	if got := usage.Token(ContextCategoryInstructions); got != 2127 {
		t.Fatalf("instructions = %d, want 2127 (its share of 3500)", got)
	}
	if usage.ConversationCapped {
		t.Fatal("scaling the rendered rows is not a capped conversation row")
	}
}

// TestScaleToKeepsTheSumExact pins the rounding: the scaled slices add up to
// the total exactly, and the tokens that flooring drops go to the categories
// whose fractional parts came closest to a whole — not to whichever id map
// iteration happened to visit last.
func TestScaleToKeepsTheSumExact(t *testing.T) {
	values := map[string]int{
		ContextCategoryConversation: 58,
		ContextCategoryInstructions: 29,
		ContextCategoryTurnContext:  13,
	}
	scaleTo(values, 37)

	// 58/100, 29/100 and 13/100 of 37 are 21.46, 10.73 and 4.81: floors
	// 21/10/4 leave two tokens, which go to turn_context (.81) and
	// instructions (.73).
	want := map[string]int{
		ContextCategoryConversation: 21,
		ContextCategoryInstructions: 11,
		ContextCategoryTurnContext:  5,
	}
	for id, tokens := range want {
		if got := values[id]; got != tokens {
			t.Fatalf("%s = %d, want %d (values = %+v)", id, got, tokens, values)
		}
	}
	sum := 0
	for _, tokens := range values {
		sum += tokens
	}
	if sum != 37 {
		t.Fatalf("sum = %d, want the total 37 exactly", sum)
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
// repair: a document stored by a build that wrote the estimates as it
// estimated them (the rows of a folding turn, a generation old) holds slices
// that sum past its own measurement. Without normalizing on read, this
// conversation would keep drawing a prompt larger than the window until
// someone ran another turn on it. The rows the harness rendered are repaired
// at their own size — the whole point of settling the conversation row first
// — and the conversation takes what is left.
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
	if !got.ConversationCapped {
		t.Fatal("the repaired snapshot did not mark its conversation row as a remainder")
	}
	if instr := got.Token(ContextCategoryInstructions); instr != 2437 {
		t.Fatalf("instructions = %d, want its stored estimate 2437 intact (%+v)",
			instr, got.Categories)
	}
	if conv := got.Token(ContextCategoryConversation); conv != 89772-2437 {
		t.Fatalf("conversation = %d, want the 87335 the measurement left (%+v)",
			conv, got.Categories)
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
