package automations

import (
	"github.com/GizClaw/flowcraft/core/agent"
)

// SettledRun is the outcome of one finished agent run, as a runner
// reports it: the record the manager persists plus the fields the
// terminal turn event carries.
type SettledRun struct {
	// Result is the run record; Status is one of the Run* constants
	// and Error is set exactly when the run did not complete.
	Result RunResult
	// Status is the engine status the run settled with, "unknown" when
	// the wait produced no result at all.
	Status agent.Status
	// ErrorText is the rendered terminal error: the engine error when
	// the result carries one (it names the failing operation), the wait
	// error otherwise. Empty on a completed run.
	ErrorText string
}

// ClassifySettledRun maps one settled (result, wait error) pair to the
// run record and the terminal fields. It is the single mapping the
// desktop runner and the host integration tests share: a run counts as
// completed only when the wait returned without error *and* the engine
// reported completion. That is the distinction the timeout handling
// depends on — a run the manager's deadline cut short settles as
// cancelled, must be recorded as failed here, and is rewritten to
// RunTimeout by the manager afterwards; a runner that reported it as
// completed would keep the record lying about what happened.
func ClassifySettledRun(
	conversationID, runID string, res *agent.Result, waitErr error,
) SettledRun {
	out := SettledRun{
		Result: RunResult{
			ConversationID: conversationID,
			RunID:          runID,
		},
		Status: agent.Status("unknown"),
	}
	if res != nil {
		out.Status = res.Status
		if res.Err != nil {
			out.ErrorText = res.Err.Error()
		}
	}
	if waitErr != nil && out.ErrorText == "" {
		out.ErrorText = waitErr.Error()
	}
	if waitErr == nil && res != nil && out.Status == agent.StatusCompleted {
		out.Result.Status = RunCompleted
		return out
	}
	out.Result.Status = RunFailed
	out.Result.Error = out.ErrorText
	return out
}
