package userstore

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// The cap holds under concurrent writers, not just sequential ones. It
// is checked inside the Add transaction, and the database hands the
// store a single connection (db.SetMaxOpenConns(1)), so a second add
// waits for the first transaction to commit and then reads the count it
// left behind — the check can never act on a snapshot another add is
// about to invalidate. (A second process writing the same file is what
// SQLite's own locking covers; the app holds a single-instance lock for
// that.)
func TestAddCapHoldsUnderConcurrentWriters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := 0; i < MaxItems-1; i++ {
		if _, err := s.Add(ctx, Fact{
			Text:  "fact " + strconv.Itoa(i),
			Scope: ScopeGlobal,
		}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Add(ctx, Fact{
				Text:  "racing " + strconv.Itoa(i),
				Scope: ScopeGlobal,
			})
		}(i)
	}
	wg.Wait()

	// One writer finds the last free slot; the others are refused by
	// the cap instead of inserting past it.
	added := 0
	for _, err := range errs {
		switch {
		case err == nil:
			added++
		case errdefs.IsValidation(err):
		default:
			t.Fatalf("unexpected error from a concurrent add: %v", err)
		}
	}
	if added > 1 {
		t.Fatalf("%d concurrent writers added past the cap", added)
	}
	count, err := s.Count(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if count != MaxItems {
		t.Fatalf("facts = %d, want the cap held at %d", count, MaxItems)
	}
}
