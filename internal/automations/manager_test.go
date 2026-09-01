package automations

import (
	"context"
	"path/filepath"
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
	store, err := Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
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
	store, err := Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
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
	store, err := Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
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
