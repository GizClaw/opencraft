package automations

import (
	"context"
	"errors"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
)

// TestClassifySettledRun pins how one finished run becomes a run
// record, especially the case the timeout handling rests on: a run the
// runner cut short (cancelled by the manager's deadline, or failed) is
// never reported as completed, whatever engine status it settled with.
func TestClassifySettledRun(t *testing.T) {
	cancelled := agent.Interrupted(agent.Interrupt{Cause: agent.CauseUserCancel})
	for _, tc := range []struct {
		name       string
		res        *agent.Result
		waitErr    error
		wantStatus RunStatus
		wantError  string
		wantEngine agent.Status
	}{
		{
			name:       "completed",
			res:        &agent.Result{Status: agent.StatusCompleted},
			wantStatus: RunCompleted,
			wantEngine: agent.StatusCompleted,
		},
		{
			name: "engine failure",
			res: &agent.Result{
				Status: agent.StatusFailed,
				Err:    errors.New("provider exploded"),
			},
			wantStatus: RunFailed,
			wantError:  "provider exploded",
			wantEngine: agent.StatusFailed,
		},
		{
			// The deadline path: WaitBounded cancelled the turn, the
			// turn settled as cancelled, and the wait itself may also
			// have returned the deadline error. The engine error wins
			// the text; the status is failed so the manager can rewrite
			// it to RunTimeout.
			name: "cancelled at the deadline",
			res: &agent.Result{
				Status: agent.StatusCanceled,
				Err:    cancelled,
			},
			waitErr:    context.DeadlineExceeded,
			wantStatus: RunFailed,
			wantError:  cancelled.Error(),
			wantEngine: agent.StatusCanceled,
		},
		{
			// No result: the wait itself failed before the turn
			// settled. Never completed, and the wait error is the only
			// story there is.
			name:       "no result",
			waitErr:    errors.New("context canceled"),
			wantStatus: RunFailed,
			wantError:  "context canceled",
			wantEngine: agent.Status("unknown"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifySettledRun("s-1", "r-1", tc.res, tc.waitErr)
			if got.Result.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Result.Status, tc.wantStatus)
			}
			if got.Result.Error != tc.wantError {
				t.Fatalf("error = %q, want %q", got.Result.Error, tc.wantError)
			}
			if got.Status != tc.wantEngine {
				t.Fatalf("engine status = %q, want %q", got.Status, tc.wantEngine)
			}
			if got.Result.ConversationID != "s-1" || got.Result.RunID != "r-1" {
				t.Fatalf("identifiers lost: %+v", got.Result)
			}
			if tc.wantStatus == RunCompleted && got.Result.Error != "" {
				t.Fatalf("completed run carries an error: %q", got.Result.Error)
			}
		})
	}
}
