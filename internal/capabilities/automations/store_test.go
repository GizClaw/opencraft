package automations

import (
	"context"
	"testing"
	"time"
)

func testTask() Task {
	return Task{
		Name:      "morning-brief",
		Prompt:    "生成简报，不要提问",
		Schedule:  Schedule{Type: ScheduleDaily, Time: "09:00"},
		Workspace: testWorkspacePath(),
		Mode:      ModeWorkspace,
		Enabled:   true,
	}
}

// testWorkspacePath returns a stable absolute path for validation (the store
// itself does not check existence; the desktop layer does).
func testWorkspacePath() string {
	return "/Users/test/projects/opencraft"
}

func TestStoreSaveTask(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("task id not generated")
	}
	if saved.Mode != ModeWorkspace {
		t.Fatalf("mode = %q, want workspace default", saved.Mode)
	}
	if !saved.NextRunAt.After(time.Now()) {
		t.Fatalf("nextRunAt %v is not in the future", saved.NextRunAt)
	}

	got, err := store.GetTask(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "morning-brief" || got.Enabled != true {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	if _, err := store.GetTask(ctx, "t-missing"); err != ErrNotFound {
		t.Fatalf("GetTask(missing) err = %v, want ErrNotFound", err)
	}
}

func TestStoreSaveTaskSetsAndPreservesWeeklyOrigin(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	task := testTask()
	task.Schedule = Schedule{
		Type:          ScheduleWeekly,
		Days:          []string{"MO"},
		Time:          "09:00",
		IntervalWeeks: 2,
	}
	saved, err := store.SaveTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Schedule.Origin == "" {
		t.Fatal("new weekly task must get a phase origin")
	}
	origin := saved.Schedule.Origin

	// An update without an origin must keep the stored phase.
	saved.Prompt = "更新后的提示词"
	saved.Schedule.Origin = ""
	updated, err := store.SaveTask(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Schedule.Origin != origin {
		t.Fatalf("origin = %q, want preserved %q", updated.Schedule.Origin, origin)
	}
	if updated.Schedule.IntervalWeeks != 2 {
		t.Fatalf("interval weeks = %d, want 2", updated.Schedule.IntervalWeeks)
	}
}

func TestStoreSaveTaskConversationID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	task := testTask()
	task.ConversationID = "s-abc123"
	saved, err := store.SaveTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConversationID != "s-abc123" {
		t.Fatalf("conversation id = %q, want s-abc123", got.ConversationID)
	}
}

func TestStoreSaveTaskRecomputesStaleAnchor(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	// Force the anchor into the past, then re-save (e.g. an edit):
	// the store must compute a fresh future trigger.
	stale := time.Now().Add(-time.Hour)
	if err := store.AdvanceNextRun(ctx, saved.ID, stale); err != nil {
		t.Fatal(err)
	}
	saved.Prompt = "更新后的提示词"
	updated, err := store.SaveTask(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.NextRunAt.After(time.Now()) {
		t.Fatalf("stale anchor not recomputed: %v", updated.NextRunAt)
	}
}

func TestStoreDeleteTaskRemovesRuns(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.AppendRun(ctx, Run{
		TaskID: saved.ID,
		At:     time.Now(),
		Status: RunRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTask(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTask(ctx, saved.ID); err != ErrNotFound {
		t.Fatalf("task still present after delete: %v", err)
	}
	runs, err := store.ListRuns(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs remain after delete: %+v", runs)
	}
	_ = run
}

func TestStorePruneRuns(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-200 * time.Hour)
	for i := 0; i < 105; i++ {
		if _, err := store.AppendRun(ctx, Run{
			TaskID: saved.ID,
			At:     base.Add(time.Duration(i) * time.Hour),
			Status: RunCompleted,
		}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := store.ListRuns(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != perTaskRunLimit {
		t.Fatalf("runs = %d, want %d", len(runs), perTaskRunLimit)
	}
	// Newest first.
	for i := 1; i < len(runs); i++ {
		if runs[i].At.After(runs[i-1].At) {
			t.Fatalf("runs not sorted newest first at %d", i)
		}
	}
}

func TestStoreReconcileStaleRuns(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.AppendRun(ctx, Run{
		TaskID: saved.ID,
		At:     time.Now().Add(-time.Hour),
		Status: RunRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := store.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled = %d, want 1", n)
	}
	runs, err := store.ListRuns(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != RunFailed || runs[0].Error != "interrupted_by_app_restart" {
		t.Fatalf("stale run not reconciled: %+v", runs[0])
	}
	_ = run
}

func TestStoreUpdateRun(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	saved, err := store.SaveTask(ctx, testTask())
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.AppendRun(ctx, Run{
		TaskID: saved.ID,
		At:     time.Now(),
		Status: RunRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	run.Status = RunCompleted
	run.ConversationID = "s-abc"
	run.RunID = "run-core"
	run.DurationMs = 1234
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := runs[0]
	if got.Status != RunCompleted || got.ConversationID != "s-abc" ||
		got.RunID != "run-core" || got.DurationMs != 1234 {
		t.Fatalf("run not updated: %+v", got)
	}
}
