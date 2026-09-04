package automations

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// RunFunc executes one task to completion and reports the result. The
// desktop layer implements it with an internal startTurn call plus a
// wait on the turn; it must not return before the run is finished.
type RunFunc func(ctx context.Context, task Task) (RunResult, error)

// ManagerOptions configures the scheduler.
type ManagerOptions struct {
	// Run executes one task (required).
	Run RunFunc
	// Now returns the current time (defaults to time.Now).
	Now func() time.Time
	// Window is the overdue grace period before an occurrence counts
	// as missed (default 2 minutes).
	Window time.Duration
	// Limit is the global concurrent run cap (default 4).
	Limit int
	// OnChange is called when task/run state changed enough that the
	// UI should refresh (best-effort).
	OnChange func()
	// OnRun is called after a run starts and after it finishes
	// (best-effort).
	OnRun func(Run)
}

// Manager owns the scan loop, the pending queue, and the concurrency
// slots. It is goroutine-safe and has no desktop dependency.
type Manager struct {
	store *Store
	runFn RunFunc
	now   func() time.Time

	window time.Duration
	limit  int

	onChange func()
	onRun    func(Run)

	mu         sync.Mutex
	pending    []string
	pendingSet map[string]bool
	running    map[string]bool
	started    bool
	stopped    bool
	stopCh     chan struct{}
	doneCh     chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewManager builds a scheduler over store. Run is required.
func NewManager(store *Store, opts ManagerOptions) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("automations: manager needs a store")
	}
	if opts.Run == nil {
		return nil, fmt.Errorf("automations: manager needs a Run function")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	window := opts.Window
	if window <= 0 {
		window = 2 * time.Minute
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 4
	}
	return &Manager{
		store:      store,
		runFn:      opts.Run,
		now:        now,
		window:     window,
		limit:      limit,
		onChange:   opts.OnChange,
		onRun:      opts.OnRun,
		pendingSet: make(map[string]bool),
		running:    make(map[string]bool),
	}, nil
}

// Start begins the 15-second scan loop and reconciles stale running
// records left by a previous app lifetime.
func (m *Manager) Start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.stopped = false
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	// Manager-owned lifecycle: the scan loop and its one-shot store
	// helpers live as long as the manager, not as long as one request.
	m.ctx, m.cancel = context.WithCancel(context.Background())
	ctx := m.ctx
	m.mu.Unlock()

	if n, err := m.store.Reconcile(ctx); err == nil && n > 0 {
		m.notifyChange()
	} else if err != nil {
		telemetry.WarnErr(ctx, "automations: reconcile stale runs failed", err)
	}
	go func() {
		defer close(m.doneCh)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Tick()
			}
		}
	}()
}

// Stop halts the scan loop. Runs already executing keep running; the
// desktop layer tears the runtime down afterwards and the next start
// reconciles their records.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.started = false
	m.stopped = true
	m.pending = nil
	m.pendingSet = make(map[string]bool)
	close(m.stopCh)
	if m.cancel != nil {
		m.cancel()
	}
	m.ctx = nil
	m.cancel = nil
	ch := m.doneCh
	m.mu.Unlock()
	<-ch
}

// Task returns one task (delegating to the store).
func (m *Manager) Task(id string) (Task, error) {
	return m.store.GetTask(m.managerContext(), id)
}

// RunNow queues one task immediately without moving its scheduled
// anchor. It rejects tasks that are already pending or running.
func (m *Manager) RunNow(taskID string) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errors.New("automations: manager is stopped")
	}
	if m.pendingSet[taskID] || m.running[taskID] {
		m.mu.Unlock()
		return errors.New("automations: task is already queued or running")
	}
	m.pendingSet[taskID] = true
	m.pending = append(m.pending, taskID)
	m.mu.Unlock()
	m.dispatch(m.managerContext())
	return nil
}

// Discard removes a task id from the pending queue (used when the
// task is deleted). A run already in flight is left alone; its record
// simply disappears with the task.
func (m *Manager) Discard(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pendingSet[taskID] {
		return
	}
	delete(m.pendingSet, taskID)
	for i, id := range m.pending {
		if id == taskID {
			m.pending = append(m.pending[:i], m.pending[i+1:]...)
			break
		}
	}
}

// Tick scans every enabled task once: due tasks within the window are
// enqueued and their anchor advanced; overdue tasks (missed while the
// app was away) only get their anchor advanced. It then dispatches as
// many queued tasks as slots allow.
func (m *Manager) Tick() {
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return
	}
	ctx := m.managerContext()
	now := m.now()
	tasks, err := m.store.ListTasks(ctx)
	if err != nil {
		telemetry.WarnErr(ctx, "automations: list tasks failed", err)
		return
	}
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		m.mu.Lock()
		busy := m.pendingSet[task.ID] || m.running[task.ID]
		m.mu.Unlock()
		if busy {
			continue
		}
		if task.NextRunAt.IsZero() || task.NextRunAt.After(now) {
			continue
		}
		next, err := task.Schedule.Next(now)
		if err != nil {
			// A corrupt schedule should not wedge the task: push the
			// anchor a day out and let the user fix it.
			telemetry.WarnErr(ctx,
				"automations: advance corrupt task schedule failed", err,
				otellog.String("task.id", task.ID))
			telemetry.WarnErr(ctx,
				"automations: advance corrupt task anchor failed",
				m.store.AdvanceNextRun(ctx, task.ID, now.AddDate(0, 0, 1)),
				otellog.String("task.id", task.ID))
			continue
		}
		if now.Sub(task.NextRunAt) > m.window {
			// Missed while the app was not running: no catch-up run,
			// just move to the next occurrence.
			telemetry.WarnErr(ctx, "automations: advance missed task failed",
				m.store.AdvanceNextRun(ctx, task.ID, next),
				otellog.String("task.id", task.ID))
			m.notifyChange()
			continue
		}
		if err := m.store.AdvanceNextRun(ctx, task.ID, next); err != nil {
			telemetry.WarnErr(ctx, "automations: advance due task failed", err,
				otellog.String("task.id", task.ID))
			continue
		}
		m.mu.Lock()
		m.pendingSet[task.ID] = true
		m.pending = append(m.pending, task.ID)
		m.mu.Unlock()
		m.notifyChange()
	}
	m.dispatch(ctx)
}

// managerContext returns the manager-owned context after Start, or a
// detached background context for one-shot operations before Start.
func (m *Manager) managerContext() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != nil {
		return m.ctx
	}
	// Before Start there is no manager lifecycle yet; a detached
	// context keeps store helpers (and unit tests) usable.
	return context.Background()
}

// dispatch starts queued runs until the slot limit is reached.
func (m *Manager) dispatch(ctx context.Context) {
	for {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		if len(m.running) >= m.limit || len(m.pending) == 0 {
			m.mu.Unlock()
			return
		}
		id := m.pending[0]
		m.pending = m.pending[1:]
		delete(m.pendingSet, id)
		m.running[id] = true
		m.mu.Unlock()
		// A Stop cancels the manager context but in-flight runs must
		// continue; detach the run from the manager's lifecycle.
		go m.run(context.WithoutCancel(ctx), id)
	}
}

func (m *Manager) run(ctx context.Context, taskID string) {
	defer func() {
		m.mu.Lock()
		delete(m.running, taskID)
		m.mu.Unlock()
		m.dispatch(ctx)
	}()

	task, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		telemetry.WarnErr(ctx, "automations: load task for run failed", err,
			otellog.String("task.id", taskID))
		return
	}
	at := m.now()
	run := Run{
		ID:     NewID("run_"),
		TaskID: taskID,
		At:     at,
		Status: RunRunning,
	}
	run, err = m.store.AppendRun(ctx, run)
	if err != nil {
		telemetry.WarnErr(ctx, "automations: append run failed", err,
			otellog.String("task.id", taskID))
		return
	}
	m.notifyRun(run)

	res, runErr := m.runFn(ctx, task)
	run.Status = RunFailed
	if runErr == nil && res.Status != "" {
		run.Status = res.Status
	}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	run.Error = res.Error
	run.DurationMs = m.now().Sub(at).Milliseconds()
	run.ConversationID = res.ConversationID
	run.RunID = res.RunID

	telemetry.WarnErr(ctx, "automations: persist run result failed",
		m.store.UpdateRun(ctx, run),
		otellog.String("task.id", taskID), otellog.String("run.id", run.ID))
	telemetry.WarnErr(ctx, "automations: persist task last run failed",
		m.store.SetTaskLast(ctx, taskID, at, string(run.Status)),
		otellog.String("task.id", taskID), otellog.String("run.id", run.ID))
	m.notifyRun(run)
	m.notifyChange()
}

func (m *Manager) notifyChange() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *Manager) notifyRun(run Run) {
	if m.onRun != nil {
		m.onRun(run)
	}
}
