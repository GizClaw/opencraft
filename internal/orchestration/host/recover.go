package host

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

// hostProcessStart is the moment this process began, used to tell a
// checkpoint left behind by a crashed run from one a sibling process
// may still be writing: the desktop app and `opencraft run` share the
// same workspace state root.
var hostProcessStart = time.Now()

// Terminal vocabulary of a recovered turn. The status is the engine's
// interrupted status, and the cause names the only interruption the
// engine could not classify itself: the process was gone before it
// could (see ClassifyRunError for the in-process causes).
const (
	statusInterrupted        = "interrupted"
	interruptCauseAppRestart = "app_restart"
)

// maxRecoveredRuns bounds one recovery pass. The scan only sees runs
// that were never archived, so a large number means something else is
// wrong (a stuck store, a corrupted workspace); the cap keeps startup
// bounded and leaves the rest for the next pass.
const maxRecoveredRuns = 50

// RecoveryReport summarizes one crash-recovery pass. It is what the
// diagnostics card shows: how many turns came back, how many
// checkpoints were only leftovers of already-archived turns, and why
// the remaining ones were left alone.
type RecoveryReport struct {
	// At is when the pass ran (zero when no pass has run yet).
	At time.Time
	// Recovered counts turns materialized as interrupted.
	Recovered int
	// Archived counts checkpoints dropped because the turn already had
	// an archive row (an in-process cancel/interrupt the observer
	// persisted before the checkpoint could be dropped).
	Archived int
	// Discarded counts checkpoints dropped as unusable: the id was not
	// a conversation, the conversation was deleted, or the board no
	// longer carried a reconstructable turn.
	Discarded int
	// SkippedLive counts checkpoints written after this process started:
	// a sibling process may still own the run, so the pass leaves them.
	SkippedLive int
	// WorkspaceHolder names the live process that owns this workspace,
	// when another process held its advisory lock and this one therefore
	// ran no pass at all (see claimWorkspaceLock). Empty means this
	// process owns the workspace.
	WorkspaceHolder string
	// Failed counts checkpoints whose recovery write failed. They stay
	// in the store for the next pass.
	Failed int
	// Pending counts checkpoints the pass did not examine (the cap).
	Pending int
}

// recoverInterruptedRuns materializes the turns a previous process never
// archived. A run writes one checkpoint per completed wave and the host
// drops it once the turn has an archive row; a SIGKILL leaves the last
// checkpoint behind with no archive row, and the conversation then has
// no trace of the turn at all — the user message exists only on that
// board, and the rollout journal never records it. Recovery turns such
// a checkpoint into an interrupted turn: archive + memory in one
// transaction under the original run id, then the terminal status.
//
// It does not replay the run. Resuming a crashed frontier means
// re-executing whatever the interrupted wave was doing, and OpenCraft's
// turns run side-effecting tools; the product promise is "the turn is
// visible and the user can continue with a fresh one", not "the turn
// continues by itself" (see docs/agent-runtime-parity/layer-1-4).
//
// Recovery is best-effort and never fails assembly: a store that cannot
// be scanned leaves the checkpoints for the next pass.
func (h *Host) recoverInterruptedRuns(ctx context.Context) {
	if h == nil || h.store == nil {
		return
	}
	report := RecoveryReport{At: time.Now().UTC()}
	defer func() { h.setRecoveryReport(report) }()

	stateStore := h.store.State()
	ids, err := stateStore.List(ctx)
	if err != nil {
		telemetry.WarnErr(ctx, "host: list run checkpoints failed", err)
		return
	}
	runs := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.HasPrefix(id, state.RunCheckpointPrefix) {
			runs = append(runs, id)
		}
	}
	if len(runs) == 0 {
		return
	}

	sink := h.memorySink()
	examined := 0
	for _, runID := range runs {
		if ctx.Err() != nil || examined >= maxRecoveredRuns {
			report.Pending++
			continue
		}
		examined++
		h.recoverRun(ctx, stateStore, sink, runID, &report)
	}
	if report.Recovered > 0 || report.Failed > 0 || report.Pending > 0 {
		telemetry.Info(ctx, "host: crash recovery pass finished",
			otellog.String("workspace", h.workDir),
			otellog.Int("recovered", report.Recovered),
			otellog.Int("archived_leftovers", report.Archived),
			otellog.Int("discarded", report.Discarded),
			otellog.Int("skipped_live", report.SkippedLive),
			otellog.String("workspace_holder", report.WorkspaceHolder),
			otellog.Int("failed", report.Failed),
			otellog.Int("pending", report.Pending))
	}
}

// recoverRun handles one run checkpoint. Every path either writes the
// turn or removes a checkpoint that can never become one.
func (h *Host) recoverRun(
	ctx context.Context,
	stateStore *state.Store,
	sink corememory.TurnSink,
	runID string,
	report *RecoveryReport,
) {
	cp, err := stateStore.Load(ctx, runID)
	if err != nil {
		telemetry.WarnErr(ctx, "host: load run checkpoint for recovery failed",
			err, otellog.String("run.id", runID))
		report.Failed++
		return
	}
	if cp == nil {
		return
	}
	// A checkpoint written after this process started may belong to a
	// sibling process that is still running the turn. Leave it: the
	// next process to open this store decides.
	if cp.Timestamp.After(hostProcessStart) {
		report.SkippedLive++
		return
	}

	conversationID := checkpointConversationID(cp)
	if !ocsessions.ValidID(conversationID) {
		// Delegated "ctx-" runs are ephemeral by design (their
		// contexts are never archived), and a board without the
		// conversation marker has nothing to recover into.
		report.Discarded++
		h.dropRecoveredCheckpoint(ctx, stateStore, runID, "invalid_conversation")
		return
	}
	if !h.store.Exists(conversationID) {
		report.Discarded++
		h.dropRecoveredCheckpoint(ctx, stateStore, runID, "conversation_gone")
		return
	}
	if _, _, err := stateStore.ArchiveTurnByRun(
		ctx, conversationID, runID,
	); err == nil {
		// The turn reached a terminal state and its archiver persisted
		// it before the host dropped the checkpoint (a cancel that
		// raced app shutdown, or a drop that failed). Nothing to
		// reconstruct.
		report.Archived++
		h.dropRecoveredCheckpoint(ctx, stateStore, runID, "")
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		telemetry.WarnErr(ctx, "host: look up archived turn failed",
			err, otellog.String("run.id", runID))
		report.Failed++
		return
	}

	board := agent.RestoreBoard(cp.Board)
	msgs := opmemory.ExtractTurnMessages(
		board, checkpointRequest(ctx, cp), nil,
	)
	if len(msgs) == 0 {
		report.Discarded++
		h.dropRecoveredCheckpoint(ctx, stateStore, runID, "empty_turn")
		return
	}

	// The reconstructed turn started with the original run and stopped
	// at the last completed wave, so the transcript dates the
	// interruption instead of the recovery.
	startedAt := cp.OriginalStartedAt
	if startedAt.IsZero() {
		startedAt = cp.Timestamp
	}
	if !startedAt.IsZero() {
		telemetry.WarnErr(ctx, "host: record recovered turn timing failed",
			h.store.RecordTurnTiming(
				conversationID, runID, startedAt.UTC(), startedAt.UTC()))
	}

	if sink != nil {
		err = opmemory.RecoverTurn(
			ctx, h.store, sink, conversationID, runID, msgs)
	} else {
		// No memory assembly in this deployment: the archive row is
		// still worth writing, the next turn simply has no folded
		// context for it.
		telemetry.Warn(ctx, "host: recovering turn without a memory sink",
			otellog.String("run.id", runID))
		err = h.store.AppendTurnWithRunID(
			ctx, conversationID, runID, msgs)
	}
	if err != nil {
		telemetry.WarnErr(ctx, "host: recover interrupted turn failed", err,
			otellog.String("conversation.id", conversationID),
			otellog.String("run.id", runID))
		report.Failed++
		return
	}
	finishedAt := cp.Timestamp
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	telemetry.WarnErr(ctx, "host: mark recovered turn interrupted failed",
		h.store.RecordTurnEnd(
			conversationID, runID, finishedAt,
			statusInterrupted, "", interruptCauseAppRestart, "", "", ""))
	report.Recovered++
	h.dropRecoveredCheckpoint(ctx, stateStore, runID, "")
	telemetry.Info(ctx, "host: recovered interrupted turn",
		otellog.String("conversation.id", conversationID),
		otellog.String("run.id", runID),
		otellog.Int("steps", len(cp.Steps)),
		otellog.Int("iteration", cp.Iteration))
}

// dropRecoveredCheckpoint deletes one checkpoint a recovery pass has
// settled. reason is recorded for the discarded ones so a checkpoint
// that can never become a turn is distinguishable from a successful
// recovery in the logs.
func (h *Host) dropRecoveredCheckpoint(
	ctx context.Context,
	stateStore *state.Store,
	runID, reason string,
) {
	if err := stateStore.Delete(ctx, runID); err != nil {
		telemetry.WarnErr(ctx, "host: delete recovered checkpoint failed",
			err, otellog.String("run.id", runID))
		return
	}
	if reason != "" {
		telemetry.Info(ctx, "host: dropped unrecoverable run checkpoint",
			otellog.String("run.id", runID),
			otellog.String("reason", reason))
	}
}

// memorySink resolves the assembled memory assembly as a turn sink, or
// nil when the deployment has none (recovery then only archives).
func (h *Host) memorySink() corememory.TurnSink {
	if h == nil || h.ctrl == nil || h.ctrl.Runtime() == nil {
		return nil
	}
	value, ok := h.ctrl.Runtime().Resource("mem")
	if !ok {
		return nil
	}
	sink, ok := value.(corememory.TurnSink)
	if !ok {
		return nil
	}
	return sink
}

// setRecoveryReport stores one pass summary for the diagnostics card.
func (h *Host) setRecoveryReport(report RecoveryReport) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.recovery = report
	h.mu.Unlock()
}

// RecoveryReport returns the last crash-recovery pass this Host ran.
// ok is false when no pass has completed yet (a freshly assembled Host
// before its first pass, or a workspace whose store has no leftovers).
func (h *Host) RecoveryReport() (RecoveryReport, bool) {
	if h == nil {
		return RecoveryReport{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.recovery.At.IsZero() {
		return RecoveryReport{}, false
	}
	return h.recovery, true
}
