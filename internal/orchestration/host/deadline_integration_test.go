package host_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// deadlineWatchdogGrace is how long a broken watchdog is given to prove
// itself before the held provider response is released anyway: with the
// watchdog working the wait ends at the deadline, without it the wait
// only ends here (and the assertion says so instead of hanging).
const deadlineWatchdogGrace = 5 * time.Second

// TestWaitBoundedCancelsAtDeadlineAndWaitsForSettle pins the contract
// an unattended run needs: the deadline cancels the live turn, and the
// wait still ends only once that turn settled (finish timing measured,
// turn archived), never at the deadline itself.
func TestWaitBoundedCancelsAtDeadlineAndWaitsForSettle(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "never read"})
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "hold this turn"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	// The provider holds the first round, so the turn is genuinely live
	// when the deadline fires.
	waitSteerGate(t, hold)
	release := time.AfterFunc(deadlineWatchdogGrace, hold.Release)
	defer release.Stop()

	deadline, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	res, waitErr := run.WaitBounded(deadline)
	waited := time.Since(started)

	if waited < 250*time.Millisecond {
		t.Fatalf("WaitBounded returned after %s, before its deadline", waited)
	}
	if waited >= deadlineWatchdogGrace {
		t.Fatalf("WaitBounded returned after %s: the deadline never "+
			"cancelled the run", waited)
	}
	if res == nil {
		t.Fatalf("WaitBounded gave no result (err %v)", waitErr)
	}
	if res.Status == agent.StatusCompleted {
		t.Fatalf("the cut-off run reports %q", res.Status)
	}
	// The settle path ran before WaitBounded returned: Wait publishes
	// the finish timing, and the settled turn is readable from the
	// archive. Both are empty while a turn is still live.
	finishedAt, durationMs := run.FinishedTiming()
	if finishedAt.IsZero() || durationMs <= 0 {
		t.Fatalf("finish timing = %v / %dms: the wait ended before the settle",
			finishedAt, durationMs)
	}
	turn, err := h.Sessions().TurnByRunID(ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("the cut-off turn is not archived: %v", err)
	}
	if turn.Status == "completed" {
		t.Fatalf("archived turn status = %q for a cancelled run", turn.Status)
	}
}

// TestAutomationDeadlineMarksTimeoutAndFreesSlot drives the whole
// unattended path on a real run: the task timeout cancels the live
// turn, the run is recorded as a timeout rather than as whatever shape
// the cancellation took, and the slot comes back so the task queued
// behind it still runs.
func TestAutomationDeadlineMarksTimeoutAndFreesSlot(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(ctx, handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := automations.Attach(handle)
	if err != nil {
		t.Fatalf("attach automation store: %v", err)
	}
	// The bound has to outlast the turn's own start: prompt assembly plus
	// the first inference call reaching the provider takes ~100ms under
	// -race here (the hop is I/O-bound, so one P does not change it) and
	// several times that on a loaded runner. A bound the call can miss is
	// not just a tighter test, it is a different one: the hold below is
	// then still armed when the deadline fires, and the task queued behind
	// this one takes it instead - its run never settles, and the failure
	// reads as "the queued task never finished" instead of "the deadline
	// beat the call". That is what a 250ms bound did on CI.
	cut := saveHostAutomationTask(t, store, "cut-short", "2s")
	queued := saveHostAutomationTask(t, store, "behind-it", "30s")

	started := make(chan string, 4)
	m, err := automations.NewManager(store, automations.ManagerOptions{
		Limit:  1,
		Window: time.Minute,
		Run: func(runCtx context.Context, task automations.Task) (automations.RunResult, error) {
			started <- task.ID
			run, err := h.StartRun(runCtx, host.RunOptions{
				Message:       message.NewTextMessage(message.RoleUser, task.Prompt),
				ContextID:     task.ConversationID,
				SkipAutoTitle: true,
			})
			if err != nil {
				return automations.RunResult{Status: automations.RunFailed}, err
			}
			// The desktop runner's own mapping, not a copy of it: the
			// manager's deadline cancels the turn, the wait continues to
			// the settle, and the settled pair is classified exactly as
			// runAutomation classifies it (see runner_test.go).
			res, waitErr := run.WaitBounded(runCtx)
			return automations.ClassifySettledRun(
				run.ContextID(), run.RunID(), res, waitErr,
			).Result, nil
		},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.RunNow(cut.ID); err != nil {
		t.Fatalf("run the cut-short task: %v", err)
	}
	select {
	case id := <-started:
		if id != cut.ID {
			t.Fatalf("first automation run = %s, want %s", id, cut.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cut-short run never reached the host")
	}
	// The hold is armed for exactly one request, the cut-short turn's
	// first inference call, and that is what keeps the turn live when the
	// deadline fires. Wait for it before queueing the task behind it -
	// every other gate user in this package does, and the hold no request
	// consumes is never released by the deadline.
	waitSteerGate(t, hold)
	// Limit 1: this one only gets to run once the cut-short run settled.
	if err := m.RunNow(queued.ID); err != nil {
		t.Fatalf("queue the second task: %v", err)
	}

	runs := waitHostAutomationRuns(ctx, t, store, cut.ID, 1)
	if runs[0].Status != automations.RunTimeout {
		t.Fatalf("cut-short run status = %q (error %q), want %q",
			runs[0].Status, runs[0].Error, automations.RunTimeout)
	}
	if !strings.Contains(runs[0].Error, "timed out after") {
		t.Fatalf("cut-short run error = %q, want the timeout sentence",
			runs[0].Error)
	}
	if runs[0].RunID == "" {
		t.Fatal("cut-short run recorded no run id")
	}
	behind := waitHostAutomationRuns(ctx, t, store, queued.ID, 1)
	if behind[0].Status != automations.RunCompleted {
		t.Fatalf("queued task status = %q (error %q), want completed",
			behind[0].Status, behind[0].Error)
	}
	select {
	case id := <-started:
		if id != queued.ID {
			t.Fatalf("second automation run = %s, want %s", id, queued.ID)
		}
	default:
		t.Fatal("the queued task never started")
	}
}

// saveHostAutomationTask stores one daily task with an explicit bound.
func saveHostAutomationTask(
	t *testing.T, store *automations.Store, name, timeout string,
) automations.Task {
	t.Helper()
	task, err := store.SaveTask(context.Background(), automations.Task{
		Name:      name,
		Prompt:    "task " + name,
		Timeout:   timeout,
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: t.TempDir(),
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("save automation task %s: %v", name, err)
	}
	return task
}

// waitHostAutomationRuns polls until one task has count settled runs.
func waitHostAutomationRuns(
	ctx context.Context, t *testing.T, store *automations.Store,
	taskID string, count int,
) []automations.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs, err := store.ListRuns(ctx, taskID)
		if err != nil {
			t.Fatalf("list runs for %s: %v", taskID, err)
		}
		settled := 0
		for _, run := range runs {
			if run.Status != automations.RunRunning {
				settled++
			}
		}
		if len(runs) >= count && settled >= count {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s runs = %+v, want %d settled", taskID, runs, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
