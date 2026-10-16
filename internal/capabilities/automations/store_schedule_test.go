package automations

import (
	"context"
	"testing"
	"time"
)

// A schedule that changed decides its own next trigger. Keeping the one
// the caller sent would fire the new cadence at the old rhythm (an edit
// from daily 09:00 to hourly 20:00 would still wait for tomorrow
// morning).
func TestStoreSaveTaskRecomputesNextRunWhenTheScheduleChanges(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	daily := saved.NextRunAt
	if daily.IsZero() {
		t.Fatal("a new task must get a next trigger")
	}

	// A same-schedule edit keeps the stored trigger: the runner owns
	// that instant, and recomputing it on every edit would re-anchor it.
	saved.Prompt = "更新后的提示词"
	kept, err := store.SaveTask(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.NextRunAt.Equal(daily) {
		t.Fatalf("same-schedule edit moved the trigger: %v -> %v",
			daily, kept.NextRunAt)
	}

	// Changing the cadence does not.
	kept.Schedule = Schedule{Type: ScheduleHourly, IntervalHours: 1}
	changed, err := store.SaveTask(ctx, kept)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !changed.NextRunAt.After(now) {
		t.Fatalf("changed schedule kept a stale trigger: %v", changed.NextRunAt)
	}
	// An hourly cadence fires within the hour; the daily trigger it
	// replaced would still be waiting for tomorrow morning.
	if changed.NextRunAt.Sub(now) > time.Hour {
		t.Fatalf("next run = %v: the old daily trigger survived the edit",
			changed.NextRunAt)
	}
}

// Run history is ordered by the instant a run started, not by how its
// timestamp was spelled. A run written with a +08:00 offset and one
// written in UTC used to sort against each other as strings, so the
// local one read as newer even when it happened first.
func TestStoreListRunsOrdersByInstant(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	earlier := time.Date(2026, 10, 16, 2, 0, 0, 0,
		time.FixedZone("CST", 8*3600)) // 2026-10-15T18:00Z
	later := time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{earlier, later} {
		if _, err := store.AppendRun(ctx, Run{
			TaskID: saved.ID,
			At:     at,
			Status: RunCompleted,
		}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := store.ListRuns(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	if !runs[0].At.Equal(later) {
		t.Fatalf("newest run = %v, want %v", runs[0].At, later)
	}
	if !runs[1].At.Equal(earlier) {
		t.Fatalf("oldest run = %v, want %v", runs[1].At, earlier)
	}
}
