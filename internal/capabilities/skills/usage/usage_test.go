package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// newStore opens a user database the way both hosts do — db.Open, then
// compat.User owns the schema — and attaches the usage store to it.
func newStore(t *testing.T) (*Store, *db.DB) {
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
		t.Fatalf("attach usage store: %v", err)
	}
	return store, handle
}

// useEvent is one activation of a skill at an explicit time: the tests
// pick the timestamp because the counters and the curator read back
// through it.
func useEvent(name, scope string, at time.Time) Event {
	return Event{Name: name, Scope: scope, UsedAt: at}
}

// archiveFor builds the record a snapshot of one skill directory
// carries.
func archiveFor(id, name string, at time.Time) Archive {
	return Archive{
		ID:          id,
		Name:        name,
		Scope:       "user",
		SkillPath:   "/skills/" + name,
		ArchivePath: "/archive/" + id + ".tar.gz",
		CreatedAt:   at,
	}
}

func TestRecordAggregatesPerSkill(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	// A whole-second base keeps the RFC3339 round trip exact: a row
	// stores seconds, so a sub-second anchor would read back rounded and
	// every comparison below would be off by a fraction.
	base := time.Now().UTC().Truncate(time.Second)

	if err := store.Record(ctx, useEvent("review", "user", base.Add(-2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, useEvent("review", "user", base)); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, useEvent("plan", "builtin", base.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	// A nameless event is dropped instead of becoming an unattributable
	// row in the counters the skills page shows.
	if err := store.Record(ctx, Event{Name: "   ", UsedAt: base}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want one aggregate per skill", stats)
	}
	// Most used first, most recently used as the tie-break.
	if stats[0].Name != "review" || stats[0].Uses != 2 {
		t.Fatalf("stats[0] = %+v, want review x2", stats[0])
	}
	if !stats[0].LastUsed.Equal(base) {
		t.Fatalf("review last used = %v, want %v", stats[0].LastUsed, base)
	}
	if stats[1].Name != "plan" || stats[1].Uses != 1 {
		t.Fatalf("stats[1] = %+v, want plan x1", stats[1])
	}

	// The window drops the events before it, which is what keeps the
	// curator from counting a use from last year as a recent one.
	windowed, err := store.Stats(ctx, base.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(windowed) != 1 || windowed[0].Name != "review" ||
		windowed[0].Uses != 1 {
		t.Fatalf("windowed stats = %+v, want review x1", windowed)
	}

	// Recording usage must not invent decisions: pin and retire are the
	// user's, never a side effect of a turn.
	states, err := store.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("usage created decisions: %+v", states)
	}
}

func TestRecordStampsMissingTimeWithStoreClock(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }

	if err := store.Record(ctx, Event{Name: "review"}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || !stats[0].LastUsed.Equal(base) {
		t.Fatalf("stats = %+v, want one event at %v", stats, base)
	}
}

func TestDecisionsAreIdempotentAndIndependent(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	pinned, err := store.SetPinned(ctx, "review", "user", true)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.Pinned || pinned.Retired || pinned.Scope != "user" ||
		pinned.UpdatedAt.IsZero() {
		t.Fatalf("SetPinned = %+v", pinned)
	}
	// Deciding again must not duplicate the row (name is the primary
	// key), and an empty scope keeps the stored one.
	again, err := store.SetPinned(ctx, "review", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Pinned || again.Scope != "user" {
		t.Fatalf("second SetPinned = %+v", again)
	}
	states, err := store.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || !states["review"].Pinned {
		t.Fatalf("states = %+v", states)
	}
	retired, err := store.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("pinning retired a skill: %v", retired)
	}

	// A pin survives retiring the same skill: the two decisions share
	// one row and neither may clear the other.
	if _, err := store.SetRetired(ctx, "review", "user", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRetired(ctx, "review", "", true); err != nil {
		t.Fatal(err)
	}
	retired, err = store.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 || !retired["review"] {
		t.Fatalf("retired = %v, want review", retired)
	}
	states, err = store.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || !states["review"].Pinned || !states["review"].Retired {
		t.Fatalf("states = %+v, want pinned and retired", states)
	}

	// Restoring clears only the retirement.
	restored, err := store.SetRetired(ctx, "review", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Retired || !restored.Pinned {
		t.Fatalf("SetRetired(false) = %+v", restored)
	}
	retired, err = store.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("restored skill still retired: %v", retired)
	}

	if _, err := store.SetPinned(ctx, "  ", "", true); !errdefs.IsValidation(err) {
		t.Fatalf("nameless decision error = %v, want validation", err)
	}
}

func TestPruneDropsEventsOutsideTheWindow(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for _, at := range []time.Time{
		base.AddDate(0, 0, -100),
		base.AddDate(0, 0, -10),
		base.AddDate(0, 0, -10),
	} {
		if err := store.Record(ctx, useEvent("review", "user", at)); err != nil {
			t.Fatal(err)
		}
	}

	pruned, err := store.Prune(ctx, base.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want the one event older than the window", pruned)
	}
	stats, err := store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Uses != 2 {
		t.Fatalf("stats after prune = %+v, want review x2", stats)
	}

	// Pruning past everything empties the table; the counters then
	// report no usage at all rather than a stale aggregate.
	pruned, err = store.Prune(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 2 {
		t.Fatalf("pruned = %d, want the remaining two events", pruned)
	}
	stats, err = store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("stats after full prune = %+v, want none", stats)
	}
}

func TestArchivesRoundTripAndStayRestorable(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	older := archiveFor("review-1", "review", base.Add(-time.Hour))
	newer := archiveFor("plan-1", "plan", base)
	for _, archive := range []Archive{older, newer} {
		if err := store.RecordArchive(ctx, archive); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.GetArchive(ctx, older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != older.Name || got.Scope != older.Scope ||
		got.SkillPath != older.SkillPath ||
		got.ArchivePath != older.ArchivePath ||
		!got.CreatedAt.Equal(older.CreatedAt) {
		t.Fatalf("GetArchive = %+v, want %+v", got, older)
	}
	if !got.RestoredAt.IsZero() {
		t.Fatalf("fresh archive already marked restored: %v", got.RestoredAt)
	}

	list, err := store.ListArchives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("ListArchives = %+v, want newest first", list)
	}

	// A restore is recorded, not deleted: the snapshot stays listed so
	// the page can still show what happened to it.
	if err := store.MarkRestored(ctx, older.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetArchive(ctx, older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RestoredAt.IsZero() {
		t.Fatal("MarkRestored did not stamp the record")
	}
	list, err = store.ListArchives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListArchives after restore = %+v, want both records", list)
	}

	if _, err := store.GetArchive(ctx, "missing"); !errdefs.IsNotFound(err) {
		t.Fatalf("GetArchive(missing) error = %v, want not found", err)
	}
	if err := store.MarkRestored(ctx, "missing"); !errdefs.IsNotFound(err) {
		t.Fatalf("MarkRestored(missing) error = %v, want not found", err)
	}
	// A record without an id, a name or a snapshot path addresses
	// nothing restorable, so it is rejected rather than stored.
	for _, broken := range []Archive{
		{Name: "review", ArchivePath: "/archive/x.tar.gz"},
		{ID: "x", ArchivePath: "/archive/x.tar.gz"},
		{ID: "x", Name: "review"},
	} {
		if err := store.RecordArchive(ctx, broken); !errdefs.IsValidation(err) {
			t.Fatalf("RecordArchive(%+v) error = %v, want validation", broken, err)
		}
	}
}

// TestEmptyLifecycleIsSilent pins the no-user-database contract: every
// call is a no-op, so a runtime without user.db never fails a turn and
// never makes the skills page think a skill was used or retired.
func TestEmptyLifecycleIsSilent(t *testing.T) {
	ctx := context.Background()
	recorder := EmptyRecorder()
	if !recorder.Empty() {
		t.Fatal("EmptyRecorder must report an empty runtime")
	}
	if err := recorder.Record(ctx, Event{Name: "review"}); err != nil {
		t.Fatalf("EmptyRecorder.Record = %v", err)
	}

	lifecycle := EmptyLifecycle()
	if !lifecycle.Empty() {
		t.Fatal("EmptyLifecycle must report an empty runtime")
	}
	if err := lifecycle.Record(ctx, Event{Name: "review"}); err != nil {
		t.Fatal(err)
	}
	if stats, err := lifecycle.Stats(ctx, time.Time{}); err != nil || stats != nil {
		t.Fatalf("Stats = %+v, %v", stats, err)
	}
	// The empty lifecycle answers reads with empty results rather than
	// nils (States hands back an empty map), so the assertion is on the
	// length, not on nil.
	if states, err := lifecycle.States(ctx); err != nil || len(states) != 0 {
		t.Fatalf("States = %+v, %v", states, err)
	}
	if retired, err := lifecycle.Retired(ctx); err != nil || retired != nil {
		t.Fatalf("Retired = %+v, %v", retired, err)
	}
	if pinned, err := lifecycle.SetPinned(ctx, "review", "user", true); err != nil ||
		!pinned.Pinned {
		t.Fatalf("SetPinned = %+v, %v", pinned, err)
	}
	if retired, err := lifecycle.SetRetired(ctx, "review", "user", true); err != nil ||
		!retired.Retired {
		t.Fatalf("SetRetired = %+v, %v", retired, err)
	}
	if pruned, err := lifecycle.Prune(ctx, time.Now()); err != nil || pruned != 0 {
		t.Fatalf("Prune = %d, %v", pruned, err)
	}
	if err := lifecycle.RecordArchive(ctx, Archive{ID: "a-1"}); err != nil {
		t.Fatal(err)
	}
	if list, err := lifecycle.ListArchives(ctx); err != nil || list != nil {
		t.Fatalf("ListArchives = %+v, %v", list, err)
	}
	if _, err := lifecycle.GetArchive(ctx, "a-1"); !errdefs.IsNotFound(err) {
		t.Fatalf("GetArchive = %v, want not found", err)
	}
	if err := lifecycle.MarkRestored(ctx, "a-1"); err != nil {
		t.Fatal(err)
	}
}

// TestStoreSurvivesMigrationRerun is the compatibility assertion of the
// 009 skill-lifecycle migration: every start re-applies the whole SQL
// set over an existing user.db, so the tables and the rows an older
// build wrote have to come back untouched.
func TestStoreSurvivesMigrationRerun(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(ctx, handle); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	store, err := Attach(handle)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	event := Event{
		Name:           "review",
		Scope:          "user",
		UsedAt:         base,
		RunID:          "run-1",
		ConversationID: "s-1",
	}
	if err := store.Record(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordArchive(ctx, archiveFor("review-1", "review", base)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRetired(ctx, "plan", "user", true); err != nil {
		t.Fatal(err)
	}

	if err := compat.User(ctx, handle); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}
	again, err := Attach(handle)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := again.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Name != "review" || stats[0].Uses != 1 {
		t.Fatalf("stats after re-migration = %+v", stats)
	}
	retired, err := again.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 || !retired["plan"] {
		t.Fatalf("retired after re-migration = %v", retired)
	}
	archives, err := again.ListArchives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 || archives[0].ID != "review-1" {
		t.Fatalf("archives after re-migration = %+v", archives)
	}
	// The run attribution survives too, which is what makes a use
	// traceable back to the turn that produced it.
	var runID string
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT run_id FROM skill_usage WHERE name = 'review'`,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if runID != "run-1" {
		t.Fatalf("run id after re-migration = %q, want run-1", runID)
	}
}
