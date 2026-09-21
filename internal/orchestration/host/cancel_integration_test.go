package host_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestAutomationCancelRunStopsLiveTurnAndFreesSlot drives the panel's
// cancel action over a real run: CancelRun stops the live turn through
// the run context the manager owns, the record ends as canceled rather
// than as whatever shape the aborted turn took, and the freed slot
// lets the task queued behind it run.
func TestAutomationCancelRunStopsLiveTurnAndFreesSlot(t *testing.T) {
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
	held := saveHostAutomationTask(t, store, "held", "30s")
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
			// The desktop runner's own mapping: the cancel travels
			// through runCtx, the wait continues to the settle, and
			// the settled pair is classified exactly as runAutomation
			// classifies it (see runner_test.go).
			res, waitErr := run.WaitBounded(runCtx)
			return automations.ClassifySettledRun(
				run.ContextID(), run.RunID(), res, waitErr,
			).Result, nil
		},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.RunNow(held.ID); err != nil {
		t.Fatalf("run the held task: %v", err)
	}
	select {
	case id := <-started:
		if id != held.ID {
			t.Fatalf("first automation run = %s, want %s", id, held.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the held run never reached the host")
	}
	// The provider holds the first round, so the turn is genuinely live
	// when the cancel arrives.
	waitSteerGate(t, hold)
	// Limit 1: this one only gets to run once the held run settled.
	if err := m.RunNow(queued.ID); err != nil {
		t.Fatalf("queue the second task: %v", err)
	}

	runID := waitHostLiveRunID(ctx, t, store, held.ID)
	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	runs := waitHostAutomationRuns(ctx, t, store, held.ID, 1)
	if runs[0].Status != automations.RunCanceled {
		t.Fatalf("held run status = %q (error %q), want %q",
			runs[0].Status, runs[0].Error, automations.RunCanceled)
	}
	if runs[0].Error != "" {
		t.Fatalf("held run error = %q, want the cancellation shape dropped",
			runs[0].Error)
	}
	if runs[0].RunID == "" {
		t.Fatal("canceled run recorded no run id")
	}
	// The turn was interrupted, not completed: the settle archived the
	// canceled turn under the run id the record carries.
	turn, err := h.Sessions().TurnByRunID(
		ctx, runs[0].ConversationID, runs[0].RunID)
	if err != nil {
		t.Fatalf("the canceled turn is not archived: %v", err)
	}
	if turn.Status == "completed" {
		t.Fatalf("archived turn status = %q for a canceled run", turn.Status)
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

// waitHostLiveRunID polls one task's history until a run is recorded as
// running and returns that run's id.
func waitHostLiveRunID(
	ctx context.Context, t *testing.T, store *automations.Store,
	taskID string,
) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs, err := store.ListRuns(ctx, taskID)
		if err != nil {
			t.Fatalf("list runs for %s: %v", taskID, err)
		}
		for _, run := range runs {
			if run.Status == automations.RunRunning {
				return run.ID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s has no live run (%+v)", taskID, runs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
