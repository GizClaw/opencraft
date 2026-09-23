package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// newQueue opens a migrated user database and binds the review queue to
// it, the shape the host hands the review hook and the settings page.
func newQueue(t *testing.T) *Store {
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
		t.Fatalf("attach queue: %v", err)
	}
	return store
}

// payload is the persisted candidate shape the queue stores and the
// review hook reads back.
func payload(text string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		panic(err)
	}
	return raw
}

func TestCreateDefaultsStatusAndMintsID(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	got, inserted, err := s.Create(ctx, Suggestion{
		Kind:    KindMemory,
		Payload: payload("remember this"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !inserted {
		t.Fatal("Create reported a fresh row as a replay")
	}
	if got.Status != StatusPending {
		t.Fatalf("status = %q, want a pending default", got.Status)
	}
	if got.ID == "" {
		t.Fatal("Create did not mint an id")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = %v / %v, want both defaulted", got.CreatedAt, got.UpdatedAt)
	}
	// The row round-trips from the database, not just from memory.
	stored, err := s.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Payload) != string(got.Payload) {
		t.Fatalf("stored payload = %s, want %s", stored.Payload, got.Payload)
	}
}

func TestCreateValidatesKindAndPayload(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	if _, _, err := s.Create(ctx, Suggestion{Payload: payload("x")}); !errdefs.IsValidation(err) {
		t.Fatalf("Create(no kind) err = %v, want a validation error", err)
	}
	if _, _, err := s.Create(ctx, Suggestion{Kind: KindMemory}); !errdefs.IsValidation(err) {
		t.Fatalf("Create(no payload) err = %v, want a validation error", err)
	}
	if _, _, err := s.Create(ctx, Suggestion{
		Kind:    KindMemory,
		Payload: json.RawMessage("not json"),
	}); !errdefs.IsValidation(err) {
		t.Fatalf("Create(bad payload) err = %v, want a validation error", err)
	}
	if _, _, err := s.Create(ctx, Suggestion{
		Kind:    KindMemory,
		Payload: payload("x"),
		Status:  "unknown",
	}); !errdefs.IsValidation(err) {
		t.Fatalf("Create(bad status) err = %v, want a validation error", err)
	}
}

// TestCreateIsIdempotentForStableID pins the review's idempotency key:
// replaying the same review of the same run must not queue a candidate
// twice, so a stable id already present is left alone.
func TestCreateIsIdempotentForStableID(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	first, inserted, err := s.Create(ctx, Suggestion{
		ID: "rv-run-1-0", Kind: KindMemory, Payload: payload("first"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("the first write reported a replay")
	}
	if _, inserted, err := s.Create(ctx, Suggestion{
		ID: "rv-run-1-0", Kind: KindMemory, Payload: payload("second"),
	}); err != nil {
		t.Fatal(err)
	} else if inserted {
		t.Fatal("the replay of a stable id reported a fresh insert")
	}

	count, err := s.Count(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want the stable id to dedupe the replay", count)
	}
	stored, err := s.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Payload) != string(first.Payload) {
		t.Fatalf("replay overwrote the first payload: %s", stored.Payload)
	}
}

func TestListFiltersByStatusAndLimit(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	pending, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("p")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(ctx, Suggestion{
		Kind: KindMemory, Payload: payload("a"), Status: StatusAccepted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(ctx, pending.ID, StatusAccepted); err != nil {
		t.Fatal(err)
	}

	acceptedRows, err := s.List(ctx, StatusAccepted, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(acceptedRows) != 2 {
		t.Fatalf("accepted rows = %d, want 2", len(acceptedRows))
	}
	pendingRows, err := s.List(ctx, StatusPending, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingRows) != 0 {
		t.Fatalf("pending rows = %d, want 0", len(pendingRows))
	}
	all, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all rows = %d, want 2", len(all))
	}
	limited, err := s.List(ctx, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit=1 returned %d rows", len(limited))
	}
}

func TestListOrdersNewestFirst(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := []time.Time{base, base.Add(time.Second), base.Add(2 * time.Second)}
	i := 0
	s.now = func() time.Time {
		at := clock[i]
		i++
		return at
	}

	first, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("one")})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("two")})
	if err != nil {
		t.Fatal(err)
	}
	third, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("three")})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	want := []string{third.ID, second.ID, first.ID}
	for i := range want {
		if rows[i].ID != want[i] {
			t.Fatalf("order = [%s %s %s], want newest-first",
				rows[0].ID, rows[1].ID, rows[2].ID)
		}
	}
}

func TestGetUnknownIDReturnsErrNotFound(t *testing.T) {
	s := newQueue(t)
	if _, err := s.Get(context.Background(), "rs-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestSetStatusRecordsVerdictAndStaysDecided(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	created, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("x")})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := s.SetStatus(ctx, created.ID, StatusAccepted)
	if err != nil {
		t.Fatalf("SetStatus(accepted): %v", err)
	}
	if decided.Status != StatusAccepted {
		t.Fatalf("status = %q, want accepted", decided.Status)
	}
	// A repeated click cannot flip an accepted row into a discarded one.
	again, err := s.SetStatus(ctx, created.ID, StatusDiscarded)
	if err != nil {
		t.Fatalf("SetStatus(discarded after accepted): %v", err)
	}
	if again.Status != StatusAccepted {
		t.Fatalf("status = %q, want the first verdict to stick", again.Status)
	}
	// The verdict is durable.
	stored, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusAccepted {
		t.Fatalf("stored status = %q, want accepted", stored.Status)
	}
}

func TestSetStatusRejectsNonDecisionStatusesAndUnknownID(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	created, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(ctx, created.ID, StatusPending); !errdefs.IsValidation(err) {
		t.Fatalf("SetStatus(pending) err = %v, want a validation error", err)
	}
	if _, err := s.SetStatus(ctx, created.ID, "maybe"); !errdefs.IsValidation(err) {
		t.Fatalf("SetStatus(unknown status) err = %v, want a validation error", err)
	}
	if _, err := s.SetStatus(ctx, "rs-missing", StatusAccepted); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStatus(unknown id) err = %v, want ErrNotFound", err)
	}
}

func TestCountByStatus(t *testing.T) {
	s := newQueue(t)
	ctx := context.Background()

	if _, _, err := s.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("a")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(ctx, Suggestion{
		Kind: KindMemory, Payload: payload("b"), Status: StatusDiscarded,
	}); err != nil {
		t.Fatal(err)
	}
	total, err := s.Count(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	pending, err := s.Count(ctx, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}
}

func TestValidStatus(t *testing.T) {
	for _, status := range []string{StatusPending, StatusAccepted, StatusDiscarded} {
		if !ValidStatus(status) {
			t.Errorf("ValidStatus(%q) = false, want true", status)
		}
	}
	if ValidStatus("maybe") {
		t.Fatal("ValidStatus accepted an unknown status")
	}
}

func TestEmptyQueueDegradesQuietly(t *testing.T) {
	ctx := context.Background()
	q := Empty()
	if !q.Empty() {
		t.Fatal("Empty() queue reported non-empty")
	}
	if rows, err := q.List(ctx, "", 0); err != nil || rows != nil {
		t.Fatalf("empty List = %v, %v, want no rows and no error", rows, err)
	}
	if n, err := q.Count(ctx, StatusPending); err != nil || n != 0 {
		t.Fatalf("empty Count = %d, %v, want 0 and no error", n, err)
	}
	if _, _, err := q.Create(ctx, Suggestion{Kind: KindMemory, Payload: payload("x")}); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty Create err = %v, want NotAvailable", err)
	}
	if _, err := q.Get(ctx, "rs-x"); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty Get err = %v, want NotAvailable", err)
	}
	if _, err := q.SetStatus(ctx, "rs-x", StatusAccepted); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty SetStatus err = %v, want NotAvailable", err)
	}
}
