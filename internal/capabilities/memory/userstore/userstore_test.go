package userstore

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// newStore opens a migrated user database and binds the memory store to
// it, the shape the host hands the remember tool and the review hook.
func newStore(t *testing.T) *Store {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := Attach(handle)
	if err != nil {
		t.Fatalf("attach memory: %v", err)
	}
	return store
}

// addOne writes one global fact and returns it, failing the test on the
// unexpected error path.
func addOne(t *testing.T, s *Store, text string) Fact {
	t.Helper()
	fact, err := s.Add(context.Background(),
		Fact{Text: text, Scope: ScopeGlobal})
	if err != nil {
		t.Fatalf("add %q: %v", text, err)
	}
	return fact
}

func TestAddValidatesTextScopeAndLength(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	mustReject := func(fact Fact) {
		t.Helper()
		if _, err := s.Add(ctx, fact); !errdefs.IsValidation(err) {
			t.Fatalf("Add(%+v) err = %v, want a validation error", fact, err)
		}
	}
	mustReject(Fact{Scope: ScopeGlobal})
	mustReject(Fact{Text: "   \n ", Scope: ScopeGlobal})
	mustReject(Fact{Text: "hi"})
	mustReject(Fact{Text: "hi", Scope: "tenant"})
	mustReject(Fact{Text: "hi", Scope: ScopeWorkspace})

	// The byte limit is inclusive: a fact of exactly MaxTextBytes is
	// accepted, one byte more is rejected.
	atLimit := strings.Repeat("a", MaxTextBytes)
	if _, err := s.Add(ctx, Fact{Text: atLimit, Scope: ScopeGlobal}); err != nil {
		t.Fatalf("Add(at limit) err = %v, want success", err)
	}
	overLimit := strings.Repeat("b", MaxTextBytes+1)
	if _, err := s.Add(ctx, Fact{Text: overLimit, Scope: ScopeGlobal}); !errdefs.IsValidation(err) {
		t.Fatalf("Add(over limit) err = %v, want a validation error", err)
	}
}

func TestAddDefaultsKindAndDropsWorkspaceForGlobalScope(t *testing.T) {
	s := newStore(t)
	fact, err := s.Add(context.Background(), Fact{
		Text:      "global facts never carry a workspace",
		Scope:     ScopeGlobal,
		Workspace: "/some/where",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Kind != "fact" {
		t.Fatalf("kind = %q, want the \"fact\" default", fact.Kind)
	}
	if fact.Workspace != "" {
		t.Fatalf("workspace = %q, want it cleared for a global fact", fact.Workspace)
	}
	if fact.ID == "" || !strings.HasPrefix(fact.ID, "um-") {
		t.Fatalf("id = %q, want a minted um- id", fact.ID)
	}
}

func TestAddIsIdempotentForRestatedText(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first := addOne(t, s, "prefers ripgrep over grep")
	second := addOne(t, s, "prefers ripgrep over grep")

	if second.ID != first.ID {
		t.Fatalf("restating a fact minted a new id: %q vs %q", first.ID, second.ID)
	}
	facts, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want one row after a restated Add", len(facts))
	}
}

func TestDedupeNormalizesCaseWhitespaceAndPunctuation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first := addOne(t, s, "Uses  FZF")
	second := addOne(t, s, "  uses fzf.  ")

	if second.ID != first.ID {
		t.Fatalf("normalized restatement created a twin: %q vs %q", first.ID, second.ID)
	}
	got, err := s.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The row keeps the id but stores the latest text verbatim.
	if got.Text != "uses fzf." {
		t.Fatalf("text = %q, want the latest restatement stored as typed", got.Text)
	}
	facts, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want one row", len(facts))
	}
}

func TestDedupeSeparatesScopesAndWorkspaces(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	same := "the deploy lane runs wails3 task package"

	global := addOne(t, s, same)
	wsA, err := s.Add(ctx, Fact{Text: same, Scope: ScopeWorkspace, Workspace: "/w/a"})
	if err != nil {
		t.Fatal(err)
	}
	wsB, err := s.Add(ctx, Fact{Text: same, Scope: ScopeWorkspace, Workspace: "/w/b"})
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]bool{global.ID: true, wsA.ID: true, wsB.ID: true}
	if len(ids) != 3 {
		t.Fatalf("same text across two scopes and two workspaces collapsed: %v", ids)
	}
}

func TestAddRejectsBeyondMaxItems(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for i := 0; i < MaxItems; i++ {
		if _, err := s.Add(ctx, Fact{
			Text:  "fact " + strconv.Itoa(i),
			Scope: ScopeGlobal,
		}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := s.Add(ctx, Fact{
		Text:  "one too many",
		Scope: ScopeGlobal,
	}); !errdefs.IsValidation(err) {
		t.Fatalf("Add over MaxItems err = %v, want a validation error", err)
	}
	facts, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != MaxItems {
		t.Fatalf("facts = %d, want the cap held at %d", len(facts), MaxItems)
	}
}

func TestReplaceMovesTextAndDedupeKeyAndDetectsCollision(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	alpha := addOne(t, s, "alpha")
	beta := addOne(t, s, "beta")

	renamed, err := s.Replace(ctx, beta.ID, "gamma")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if renamed.Text != "gamma" || renamed.DedupeKey != "gamma" {
		t.Fatalf("Replace result = %+v, want text and key moved to gamma", renamed)
	}
	if renamed.ID != beta.ID {
		t.Fatalf("Replace changed the id: %q -> %q", beta.ID, renamed.ID)
	}

	// Editing beta's text onto alpha's text collides on the dedupe index
	// and must be reported as a conflict, not silently merged.
	if _, err := s.Replace(ctx, beta.ID, "alpha"); !errdefs.IsConflict(err) {
		t.Fatalf("Replace collision err = %v, want a conflict", err)
	}
	// The rejected edit left both rows untouched.
	for _, id := range []string{alpha.ID, beta.ID} {
		if _, err := s.Get(ctx, id); err != nil {
			t.Fatalf("row %q missing after a rejected Replace: %v", id, err)
		}
	}
}

func TestReplaceAndRemoveUnknownID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Replace(ctx, "um-missing", "text"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Replace(unknown) err = %v, want ErrNotFound", err)
	}
	if err := s.Remove(ctx, "um-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove(unknown) err = %v, want ErrNotFound", err)
	}
	if _, err := s.Replace(ctx, "", "text"); !errdefs.IsValidation(err) {
		t.Fatalf("Replace(empty id) err = %v, want a validation error", err)
	}
	if err := s.Remove(ctx, " "); !errdefs.IsValidation(err) {
		t.Fatalf("Remove(blank id) err = %v, want a validation error", err)
	}
}

func TestRemoveDeletesOneFact(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	fact := addOne(t, s, "temporary")
	if err := s.Remove(ctx, fact.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Get(ctx, fact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Remove err = %v, want ErrNotFound", err)
	}
}

func TestSetStaleHidesAndRevives(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	fact := addOne(t, s, "a fact to retire")

	retired, err := s.SetStale(ctx, fact.ID, true)
	if err != nil {
		t.Fatalf("SetStale(true): %v", err)
	}
	if !retired.Stale {
		t.Fatal("SetStale did not mark the fact stale")
	}
	visible, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 0 {
		t.Fatalf("retired fact still injected: %+v", visible)
	}
	all, err := s.List(ctx, Query{IncludeStale: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || !all[0].Stale {
		t.Fatalf("IncludeStale list = %+v, want the retired fact", all)
	}

	revived, err := s.SetStale(ctx, fact.ID, false)
	if err != nil {
		t.Fatalf("SetStale(false): %v", err)
	}
	if revived.Stale {
		t.Fatal("SetStale(false) did not revive the fact")
	}
	if _, err := s.SetStale(ctx, "um-missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStale(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestListFiltersByWorkspaceScopeAndLimit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	addOne(t, s, "a global fact")
	if _, err := s.Add(ctx, Fact{
		Text: "workspace A fact", Scope: ScopeWorkspace, Workspace: "/w/a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, Fact{
		Text: "workspace B fact", Scope: ScopeWorkspace, Workspace: "/w/b",
	}); err != nil {
		t.Fatal(err)
	}

	scoped, err := s.List(ctx, Query{Workspace: "/w/a"})
	if err != nil {
		t.Fatal(err)
	}
	texts := factTexts(scoped)
	if !contains(texts, "a global fact") || !contains(texts, "workspace A fact") {
		t.Fatalf("workspace A list = %v, want the global and its own fact", texts)
	}
	if contains(texts, "workspace B fact") {
		t.Fatalf("a foreign workspace fact leaked into the list: %v", texts)
	}

	limited, err := s.List(ctx, Query{Workspace: "/w/a", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("Limit=1 returned %d rows", len(limited))
	}
}

// TestListOrdersNewestFirstWithinOneSecond pins the ordering contract
// the injected memory section relies on: List returns facts by
// updated_at DESC. Two facts written in the same wall-clock second must
// still come back in write order, which is why the stored timestamp
// carries a fixed-width nanosecond fraction (RFC3339Nano trims trailing
// zeros, so ".5" would sort after ".51" and invert the pair).
func TestListOrdersNewestFirstWithinOneSecond(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	base := time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC)
	clock := []time.Time{base.Add(500 * time.Millisecond), base.Add(510 * time.Millisecond)}
	i := 0
	s.now = func() time.Time {
		at := clock[i]
		i++
		return at
	}

	older := addOne(t, s, "written first")
	newer := addOne(t, s, "written second")

	facts, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(facts))
	}
	if facts[0].ID != newer.ID || facts[1].ID != older.ID {
		t.Fatalf("order = [%s %s], want newest (%s) first",
			facts[0].Text, facts[1].Text, newer.Text)
	}
	if !facts[0].UpdatedAt.After(facts[1].UpdatedAt) {
		t.Fatalf("round-tripped timestamps not ordered: %v then %v",
			facts[0].UpdatedAt, facts[1].UpdatedAt)
	}
}

func TestListOrdersByDistinctSecondsNewestFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := []time.Time{base, base.Add(time.Second), base.Add(2 * time.Second)}
	i := 0
	s.now = func() time.Time {
		at := clock[i]
		i++
		return at
	}

	first := addOne(t, s, "one")
	second := addOne(t, s, "two")
	third := addOne(t, s, "three")

	facts, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{facts[0].ID, facts[1].ID, facts[2].ID}
	want := []string{third.ID, second.ID, first.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want newest-first %v", got, want)
		}
	}
}

func TestGetUnknownIDReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get(context.Background(), "um-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestValidateNormalizesTextAndDedupeKey(t *testing.T) {
	got, err := Validate(Fact{Text: "  Uses   FZF  ", Scope: ScopeGlobal})
	if err != nil {
		t.Fatal(err)
	}
	// The stored text only loses outer whitespace; the dedupe key
	// collapses internal runs and lowercases.
	if got.Text != "Uses   FZF" {
		t.Fatalf("text = %q, want outer whitespace trimmed only", got.Text)
	}
	if got.DedupeKey != "uses fzf" {
		t.Fatalf("dedupe key = %q, want normalized", got.DedupeKey)
	}
	if _, err := Validate(Fact{Text: "x", Scope: "nope"}); !errdefs.IsValidation(err) {
		t.Fatalf("Validate(unknown scope) err = %v, want a validation error", err)
	}
}

func TestDedupeKeyOf(t *testing.T) {
	check := func(in, want string) {
		t.Helper()
		if got := DedupeKeyOf(in); got != want {
			t.Errorf("DedupeKeyOf(%q) = %q, want %q", in, got, want)
		}
	}
	check("Hello, World!", "hello, world")
	check("  FOO  ", "foo")
	check("trailing...", "trailing")
	check("a\tb\nc", "a b c")
}

func TestEmptyMemoryDegradesQuietly(t *testing.T) {
	ctx := context.Background()
	m := Empty()
	if !m.Empty() {
		t.Fatal("Empty() memory reported non-empty")
	}
	if facts, err := m.List(ctx, Query{}); err != nil || facts != nil {
		t.Fatalf("empty List = %v, %v, want no rows and no error", facts, err)
	}
	if _, err := m.Get(ctx, "um-x"); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty Get err = %v, want NotAvailable", err)
	}
	if _, err := m.Add(ctx, Fact{Text: "x", Scope: ScopeGlobal}); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty Add err = %v, want NotAvailable", err)
	}
	if _, err := m.Replace(ctx, "um-x", "y"); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty Replace err = %v, want NotAvailable", err)
	}
	if _, err := m.SetStale(ctx, "um-x", true); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty SetStale err = %v, want NotAvailable", err)
	}
	if err := m.Remove(ctx, "um-x"); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty Remove err = %v, want NotAvailable", err)
	}
}

func factTexts(facts []Fact) []string {
	out := make([]string, 0, len(facts))
	for _, fact := range facts {
		out = append(out, fact.Text)
	}
	return out
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
