package bindings

import (
	"context"

	flowtelemetry "github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// finishTurn emits the terminal events of one turn: the turn_end the
// transcript renders from, and the status clear that follows it. Every
// turn ends here — a workspace send, an application send — because the
// two differ in scope, not in what a finished turn reports.
//
// appID names the surface the turn belongs to (empty = the window's
// workspace) and agentID the agent that ran it; both travel on the event
// so the frontend can route it without guessing. The error classification
// is host.ClassifyRunError's: a timeout and a user stop are different
// rows in the transcript, and a caller that only looked at the status
// would show one as the other.
func finishTurn(
	ctx context.Context,
	c *core.Core,
	run *host.Run,
	appID, agentID, contextID string,
) {
	res, err := run.Wait(ctx)
	status := "unknown"
	var errText string
	if res != nil {
		status = string(res.Status)
		if res.Err != nil {
			errText = res.Err.Error()
		}
	}
	if err != nil && errText == "" {
		errText = err.Error()
	}
	finishedAt, durationMs := run.FinishedTiming()
	requestID, responseID := run.FinishedIDs()
	end := core.NewTurnEnd(
		run.RunID(), appID, contextID, status, errText,
		requestID, responseID,
		lastAssistantOutput(res), finishedAt, durationMs, res,
	)
	if end.SteerPending == nil {
		flowtelemetry.Warn(context.WithoutCancel(ctx),
			"conversation: undelivered steer count unreadable; "+
				"the UI keeps every steered row",
			otellog.String("run.id", run.RunID()))
	}
	if res != nil {
		if report, ok := worldstate.CompactionReportFromBoard(res.LastBoard); ok &&
			!report.Empty() {
			end.Compaction = &core.CompactionEvent{
				Folds:    report.Folds,
				Failures: report.Failures,
				Notified: report.Notified,
			}
		}
	}
	class := host.ClassifyRunError(resultErr(res), err)
	end.InterruptCause = class.InterruptCause
	end.ErrorKind = class.ErrorKind
	end.AgentID = agentID
	c.Shell.Emit(core.EventTurnEnd, end)
	if appID == "" {
		// The window's own busy state. An application's turn is not the
		// composer's — its page reads its own runs (App.ActiveRun) — and
		// clearing the flag here would stop a spinner a workspace turn
		// still owns.
		c.Shell.Emit(core.EventStatus, core.StatusEvent{})
	}
}
