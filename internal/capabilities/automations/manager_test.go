package automations

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestManager(
	t *testing.T,
	run RunFunc,
) (*Manager, *Store, *time.Time) {
	t.Helper()
	_, store := newUserStore(t)
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local)
	m, err := NewManager(store, ManagerOptions{
		Run: run,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m, store, &now
}

func saveDailyTask(
	t *testing.T, store *Store, name string,
) Task {
	t.Helper()
	task, err := store.SaveTask(context.Background(), Task{
		Name:      name,
		Prompt:    "任务 " + name,
		Schedule:  Schedule{Type: ScheduleDaily, Time: "09:00"},
		Workspace: "/Users/test/projects/opencraft",
		Mode:      ModeWorkspace,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	return task
}

func TestManagerTriggersDueTask(t *testing.T) {
	var (
		mu      sync.Mutex
		ran     []string
		runDone = make(chan struct{}, 1)
	)
	m, store, now := newTestManager(t, func(_ context.Context, task Task) (RunResult, error) {
		mu.Lock()
		ran = append(ran, task.ID)
		mu.Unlock()
		runDone <- struct{}{}
		return RunResult{Status: RunCompleted}, nil
	})
	task := saveDailyTask(t, store, "brief")
	// Due at 09:00 today, anchored in the past so Tick consumes it.
	due := time.Date(2026, 9, 1, 8, 59, 50, 0, time.Local)
	if err := store.AdvanceNextRun(context.Background(), task.ID, due); err != nil {
		t.Fatal(err)
	}

	m.Tick()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not run")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 1 || ran[0] != task.ID {
		t.Fatalf("ran = %v, want [%s]", ran, task.ID)
	}

	got, _ := store.GetTask(context.Background(), task.ID)
	want := time.Date(2026, 9, 2, 9, 0, 0, 0, time.Local)
	if !got.NextRunAt.Equal(want) {
		t.Fatalf("nextRunAt = %v, want %v", got.NextRunAt, want)
	}
	deadline := time.Now().Add(2 * time.Second)
	for got.LastStatus != string(RunCompleted) && time.Now().Before(deadline) {
		got, _ = store.GetTask(context.Background(), task.ID)
		time.Sleep(5 * time.Millisecond)
	}
	if got.LastStatus != string(RunCompleted) {
		t.Fatalf("lastStatus = %q, want completed", got.LastStatus)
	}
	runs, _ := store.ListRuns(context.Background(), task.ID)
	if len(runs) != 1 || runs[0].Status != RunCompleted {
		t.Fatalf("runs = %+v", runs)
	}
	_ = now
}

func TestManagerMissedWindowAdvancesWithoutRun(t *testing.T) {
	var ran atomic.Int32
	m, store, _ := newTestManager(t, func(context.Context, Task) (RunResult, error) {
		ran.Add(1)
		return RunResult{Status: RunCompleted}, nil
	})
	task := saveDailyTask(t, store, "brief")
	// 5 minutes overdue: the app was away at the trigger point.
	due := time.Date(2026, 9, 1, 8, 55, 0, 0, time.Local)
	if err := store.AdvanceNextRun(context.Background(), task.ID, due); err != nil {
		t.Fatal(err)
	}
	m.Tick()
	if ran.Load() != 0 {
		t.Fatalf("missed window ran %d times, want 0", ran.Load())
	}
	got, _ := store.GetTask(context.Background(), task.ID)
	want := time.Date(2026, 9, 2, 9, 0, 0, 0, time.Local)
	if !got.NextRunAt.Equal(want) {
		t.Fatalf("nextRunAt = %v, want %v (schedule wedged)", got.NextRunAt, want)
	}
}

func TestManagerRunNowDoesNotMoveAnchor(t *testing.T) {
	release := make(chan struct{})
	ran := make(chan string, 1)
	m, store, _ := newTestManager(t, func(_ context.Context, task Task) (RunResult, error) {
		ran <- task.ID
		<-release
		return RunResult{Status: RunCompleted}, nil
	})
	task := saveDailyTask(t, store, "brief")
	before, _ := store.GetTask(context.Background(), task.ID)
	if err := m.RunNow(task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("RunNow did not run")
	}
	if err := m.RunNow(task.ID); err == nil {
		t.Fatal("second RunNow while running should fail")
	}
	close(release)
	got, _ := store.GetTask(context.Background(), task.ID)
	if !got.NextRunAt.Equal(before.NextRunAt) {
		t.Fatalf("RunNow moved the anchor: %v -> %v",
			before.NextRunAt, got.NextRunAt)
	}
}

func TestManagerConcurrencyLimit(t *testing.T) {
	const (
		tasks = 8
		limit = 4
	)
	var (
		active    atomic.Int32
		maxActive atomic.Int32
		release   = make(chan struct{})
	)
	run := func(context.Context, Task) (RunResult, error) {
		cur := active.Add(1)
		for {
			prev := maxActive.Load()
			if cur <= prev || maxActive.CompareAndSwap(prev, cur) {
				break
			}
		}
		<-release
		active.Add(-1)
		return RunResult{Status: RunCompleted}, nil
	}
	_, store := newUserStore(t)
	m, err := NewManager(store, ManagerOptions{
		Run:    run,
		Now:    func() time.Time { return time.Now() },
		Limit:  limit,
		Window: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < tasks; i++ {
		task := saveDailyTask(t, store, "task")
		if err := m.RunNow(task.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Let the first `limit` goroutines start, then verify the cap held.
	deadline := time.Now().Add(2 * time.Second)
	for maxActive.Load() < limit && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := maxActive.Load(); got != limit {
		t.Fatalf("max active = %d, want %d", got, limit)
	}
	close(release)
	deadline = time.Now().Add(2 * time.Second)
	for active.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatalf("runs did not settle: active = %d", active.Load())
	}
}

func TestManagerDisabledTaskDoesNotTrigger(t *testing.T) {
	var ran atomic.Int32
	m, store, _ := newTestManager(t, func(context.Context, Task) (RunResult, error) {
		ran.Add(1)
		return RunResult{Status: RunCompleted}, nil
	})
	task := saveDailyTask(t, store, "brief")
	task.Enabled = false
	if _, err := store.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceNextRun(context.Background(), task.ID,
		time.Date(2026, 9, 1, 8, 59, 50, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	m.Tick()
	if ran.Load() != 0 {
		t.Fatalf("disabled task ran %d times", ran.Load())
	}
}

func TestManagerStartReconcilesStaleRuns(t *testing.T) {
	_, store := newUserStore(t)
	task := saveDailyTask(t, store, "brief")
	if _, err := store.AppendRun(context.Background(), Run{
		TaskID: task.ID,
		At:     time.Now().Add(-time.Hour),
		Status: RunRunning,
	}); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(store, ManagerOptions{
		Run: func(context.Context, Task) (RunResult, error) {
			return RunResult{Status: RunCompleted}, nil
		},
		Now: func() time.Time { return time.Now() },
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	defer m.Stop()
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != RunFailed {
		t.Fatalf("stale run status = %q, want failed", runs[0].Status)
	}
}

// TestManagerStopDoesNotDispatchQueued verifies Stop clears the pending
// queue and prevents in-flight run completion from starting queued
// tasks afterwards.
func TestManagerStopDoesNotDispatchQueued(t *testing.T) {
	_, store := newUserStore(t)
	now := time.Date(2026, 9, 1, 9, 0, 10, 0, time.Local)
	started := make(chan string, 2)
	release := make(chan struct{})
	m, err := NewManager(store, ManagerOptions{
		Run: func(_ context.Context, task Task) (RunResult, error) {
			started <- task.ID
			<-release
			return RunResult{Status: RunCompleted}, nil
		},
		Now:    func() time.Time { return now },
		Limit:  1,
		Window: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	first := saveDailyTask(t, store, "first")
	second := saveDailyTask(t, store, "second")
	due := now.Add(-10 * time.Second)
	for _, id := range []string{first.ID, second.ID} {
		if err := store.AdvanceNextRun(context.Background(), id, due); err != nil {
			t.Fatal(err)
		}
	}

	m.Start()
	defer m.Stop()
	m.Tick()
	select {
	case got := <-started:
		if got != first.ID {
			t.Fatalf("first run id = %s, want %s", got, first.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not start")
	}

	// Second task becomes pending behind the running one.
	m.Tick()
	m.mu.Lock()
	pending := len(m.pending) + len(m.pendingSet)
	m.mu.Unlock()
	if pending == 0 {
		t.Fatal("second task was not queued")
	}

	m.Stop()
	close(release)
	select {
	case extra := <-started:
		t.Fatalf("queued task %s started after Stop", extra)
	case <-time.After(300 * time.Millisecond):
	}
	m.mu.Lock()
	pending = len(m.pending) + len(m.pendingSet)
	m.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending queue after Stop = %d, want 0", pending)
	}
	if err := m.RunNow(second.ID); err == nil {
		t.Fatal("RunNow after Stop must fail")
	}
}

// TestManagerRunTimeoutMarksRecordAndReleasesSlot pins the L1-2
// contract for unattended runs: the task's timeout bounds the run, the
// record says the run timed out (rather than showing the cancellation
// shape the runner reported), and the slot comes back so a task queued
// behind it still runs.
func TestManagerRunTimeoutMarksRecordAndReleasesSlot(t *testing.T) {
	ctx := context.Background()
	_, store := newUserStore(t)
	// A short timeout is a test affordance: the field accepts any
	// positive duration; only the desktop form keeps to whole minutes.
	first := saveDailyTask(t, store, "slow")
	first.Timeout = "40ms"
	first, err := store.SaveTask(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	second := saveDailyTask(t, store, "later")

	var (
		mu      sync.Mutex
		started []string
	)
	startedCh := make(chan struct{})
	m, err := NewManager(store, ManagerOptions{
		Run: func(runCtx context.Context, task Task) (RunResult, error) {
			mu.Lock()
			started = append(started, task.ID)
			mu.Unlock()
			if task.ID == first.ID {
				close(startedCh)
				<-runCtx.Done()
				return RunResult{
					Status: RunFailed,
					Error:  runCtx.Err().Error(),
				}, nil
			}
			return RunResult{Status: RunCompleted}, nil
		},
		Now:    func() time.Time { return time.Now() },
		Limit:  1,
		Window: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(first.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not start")
	}
	// The second task has to wait for the slot: with Limit 1 it can only
	// start once the timed-out run settled.
	if err := m.RunNow(second.ID); err != nil {
		t.Fatal(err)
	}

	runs, err := waitForRuns(ctx, t, store, first.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != RunTimeout {
		t.Fatalf("run status = %q (error %q), want %q",
			runs[0].Status, runs[0].Error, RunTimeout)
	}
	if !strings.Contains(runs[0].Error, "timed out after 40ms") {
		t.Fatalf("run error = %q, want the timeout sentence", runs[0].Error)
	}
	secondRuns, err := waitForRuns(ctx, t, store, second.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if secondRuns[0].Status != RunCompleted {
		t.Fatalf("queued task status = %q, want completed",
			secondRuns[0].Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(started) != 2 || started[0] != first.ID || started[1] != second.ID {
		t.Fatalf("start order = %v, want [%s %s]", started, first.ID, second.ID)
	}
}

// TestManagerTickSkipsDueTaskWhileRunning pins the duplicate-trigger
// rule: a task that comes due while its own run is still live is
// skipped, not queued behind itself. The occurrence is not lost — the
// anchor stays in the past and the next tick after the run settles
// starts exactly one run.
func TestManagerTickSkipsDueTaskWhileRunning(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	started := make(chan struct{})
	var runs atomic.Int32
	m, store, _ := newTestManager(t,
		func(context.Context, Task) (RunResult, error) {
			if runs.Add(1) == 1 {
				close(started)
			}
			<-release
			return RunResult{Status: RunCompleted}, nil
		})
	task := saveDailyTask(t, store, "brief")
	due := time.Date(2026, 9, 1, 8, 59, 50, 0, time.Local)
	if err := store.AdvanceNextRun(ctx, task.ID, due); err != nil {
		t.Fatal(err)
	}
	m.Tick()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first tick did not start the run")
	}
	// Due again while its run is live: the tick must skip it.
	if err := store.AdvanceNextRun(ctx, task.ID, due); err != nil {
		t.Fatal(err)
	}
	m.Tick()
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs while live = %d, want 1", got)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for runs.Load() < 2 && time.Now().Before(deadline) {
		m.Tick()
		time.Sleep(5 * time.Millisecond)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("runs after the run settled = %d, want 2", got)
	}
	if _, err := waitForRuns(ctx, t, store, task.ID, 2); err != nil {
		t.Fatal(err)
	}
}

// TestManagerCancelRunMarksCanceledAndFreesSlot pins the user-cancel
// path: CancelRun stops the live run through its context, the record
// ends as canceled instead of carrying the shape the cancellation took,
// and the freed slot lets the task queued behind it start.
func TestManagerCancelRunMarksCanceledAndFreesSlot(t *testing.T) {
	ctx := context.Background()
	_, store := newUserStore(t)
	first := saveDailyTask(t, store, "held")
	second := saveDailyTask(t, store, "behind")

	started := make(chan struct{})
	secondStarted := make(chan struct{})
	m, err := NewManager(store, ManagerOptions{
		Run: func(runCtx context.Context, task Task) (RunResult, error) {
			if task.ID == first.ID {
				close(started)
				<-runCtx.Done()
				return RunResult{
					Status: RunFailed,
					Error:  runCtx.Err().Error(),
				}, nil
			}
			close(secondStarted)
			return RunResult{Status: RunCompleted}, nil
		},
		Now:    func() time.Time { return time.Now() },
		Limit:  1,
		Window: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(first.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the held run never started")
	}
	if err := m.RunNow(second.ID); err != nil {
		t.Fatal(err)
	}

	runID := waitForLiveRunID(ctx, t, store, first.ID)
	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	runs, err := waitForRuns(ctx, t, store, first.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != RunCanceled {
		t.Fatalf("run status = %q (error %q), want %q",
			runs[0].Status, runs[0].Error, RunCanceled)
	}
	if runs[0].Error != "" {
		t.Fatalf("run error = %q, want the cancellation shape dropped",
			runs[0].Error)
	}
	// The slot came back: with Limit 1 the queued task can only start
	// once the canceled run settled.
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the queued task never started after the cancel")
	}
	secondRuns, err := waitForRuns(ctx, t, store, second.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if secondRuns[0].Status != RunCompleted {
		t.Fatalf("queued task status = %q, want completed",
			secondRuns[0].Status)
	}
}

// TestManagerCancelRunRejectsInactiveRun: a cancel aimed at a run that
// is not live on this manager reports an error instead of silently
// doing nothing (the run may have settled a moment earlier).
func TestManagerCancelRunRejectsInactiveRun(t *testing.T) {
	ctx := context.Background()
	m, store, _ := newTestManager(t,
		func(context.Context, Task) (RunResult, error) {
			return RunResult{Status: RunCompleted}, nil
		})
	if err := m.CancelRun("run_missing"); err == nil {
		t.Fatal("canceling an unknown run succeeded")
	}
	task := saveDailyTask(t, store, "quick")
	if err := m.RunNow(task.ID); err != nil {
		t.Fatal(err)
	}
	runs, err := waitForRuns(ctx, t, store, task.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != RunCompleted {
		t.Fatalf("run status = %q, want completed", runs[0].Status)
	}
	if err := m.CancelRun(runs[0].ID); err == nil {
		t.Fatal("canceling a finished run succeeded")
	}
}

// waitForLiveRunID polls one task's history until a run is recorded as
// running and returns that run's id.
func waitForLiveRunID(
	ctx context.Context, t *testing.T, store *Store, taskID string,
) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		runs, err := store.ListRuns(ctx, taskID)
		if err != nil {
			t.Fatalf("list runs for %s: %v", taskID, err)
		}
		for _, run := range runs {
			if run.Status == RunRunning {
				return run.ID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s has no live run (%+v)", taskID, runs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForRuns polls one task's run history until it holds count settled
// records (nothing left "running").
func waitForRuns(
	ctx context.Context, t *testing.T, store *Store,
	taskID string, count int,
) ([]Run, error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		runs, err := store.ListRuns(ctx, taskID)
		if err != nil {
			return nil, err
		}
		settled := 0
		for _, run := range runs {
			if run.Status != RunRunning {
				settled++
			}
		}
		if len(runs) >= count && settled >= count {
			return runs, nil
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s runs = %+v, want %d settled",
				taskID, runs, count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
