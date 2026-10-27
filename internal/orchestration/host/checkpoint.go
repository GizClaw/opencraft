package host

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
)

// The engine stamps one checkpoint per completed wave while a turn runs
// (flowcraft's graph "safe boundary"), and the checkpoint store upserts
// them by run id, so the row that survives a crash is the last wave the
// run finished. Those rows are a crash-recovery log, not context: they
// only exist to let the next assembly reconstruct a turn the process
// never archived (L1-4). The annotations below travel on
// agent.Request.Attributes, which flowcraft copies into Run.Attributes
// and the graph then stamps onto every checkpoint.
const (
	// checkpointAttrConversationID names the conversation a run belongs
	// to. Recovery cannot read it off the board: delegated "ctx-" runs
	// never carry a conversation var, and a wave-1 checkpoint may not
	// have run the world node yet.
	checkpointAttrConversationID = "oc.conversation_id"
	// checkpointAttrRequest carries the turn's original request JSON so
	// recovery can rebuild the user message in its pre-inline form. The
	// board holds the media-inlined variant the model saw (attachment
	// bytes inlined by the prepare hook), which must not be archived.
	checkpointAttrRequest = "oc.request"
)

// boardVarThreadID is the world node's board var naming the
// conversation; older checkpoints written before the attributes above
// existed fall back to it.
const boardVarThreadID = "oc_thread_id"

// maxRecoveryRequestBytes bounds the request JSON annotated onto a
// run's checkpoints. Attachments are persisted as URL references before
// the turn starts, so the encode is normally a few kilobytes; a request
// above the cap drops the annotation (recovery then falls back to the
// board form) instead of writing it into every checkpoint row.
const maxRecoveryRequestBytes = 64 << 10

// recoveryRequestAttributes builds the attribute bag for one turn's
// request: the conversation id every checkpoint needs, plus the request
// JSON in its original (pre-inline) form.
func recoveryRequestAttributes(request agent.Request) map[string]string {
	attrs := map[string]string{
		checkpointAttrConversationID: request.ContextID,
	}
	// Marshal the request without the bag itself and without the run id
	// core mints at start: recovery replays the request as the caller
	// handed it over.
	original := request
	original.Attributes = nil
	original.RunID = ""
	raw, err := json.Marshal(original)
	if err == nil && len(raw) <= maxRecoveryRequestBytes {
		attrs[checkpointAttrRequest] = string(raw)
	}
	return attrs
}

// checkpointConversationID reads the conversation a run checkpoint
// belongs to: the recovery annotation when present, otherwise the
// board var the world node sets. The "oc-" prefix is stripped so a
// delegated or legacy id never looks like a conversation; callers still
// validate the result with ids.IsSession.
func checkpointConversationID(cp *agent.Checkpoint) string {
	if cp == nil {
		return ""
	}
	if id := strings.TrimSpace(cp.Attributes[checkpointAttrConversationID]); id != "" {
		return id
	}
	if cp.Board == nil {
		return ""
	}
	raw, ok := agent.RestoreBoard(cp.Board).Var(boardVarThreadID)
	if !ok {
		return ""
	}
	id, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(id), "oc-")
}

// checkpointRequest decodes the original request annotated onto a
// checkpoint. A missing annotation (a checkpoint that predates it, or a
// request over the size cap) returns nil, and the caller degrades to the
// board form.
func checkpointRequest(ctx context.Context, cp *agent.Checkpoint) *agent.Request {
	if cp == nil {
		return nil
	}
	raw := cp.Attributes[checkpointAttrRequest]
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var request agent.Request
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		telemetry.WarnErr(ctx, "host: decode checkpoint request failed", err,
			attribute.String("run.id", cp.ExecID))
		return nil
	}
	return &request
}

// CheckpointCapabilities implements agent.CheckpointTarget: it is what tells
// the engine whether this host persists checkpoints and whether the store
// takes increments.
//
// The answer is the store's, not a constant: the session store implements
// agent.CheckpointJournal, so the engine writes one base per run and then only
// the board's writes at each wave. Without this method the runtime falls back
// to "persists, no increments" and re-serializes the whole board at every
// wave — the cost the journal exists to remove (a profile of one turn on a
// 45 MB conversation attributed 55% of its allocation to that path).
func (h *Host) CheckpointCapabilities() agent.CheckpointCapabilities {
	if h == nil || h.store == nil {
		return agent.CheckpointCapabilities{}
	}
	return agent.CheckpointCapabilitiesForStore(h.store)
}

var _ agent.CheckpointTarget = (*Host)(nil)

// trimCheckpoint shrinks a wave checkpoint to the turn recovery will
// reconstruct from it.
//
// Since core/v0.10.0 the store is also a checkpoint journal: the graph
// writes one base record per run and then only the board's writes at each
// wave, so this hook sees a base at the three moments a base is written
// (the first boundary, after a resume, when the chain reaches its cap).
// The trim still matters there — a base is the record that carries the
// whole board — and it is inert on deltas, which never pass through here.
//
// The graph stamps the whole board once per completed wave (see
// flowcraft's graph stampCheckpoint), and the store upserts by run id, so
// what survives is the last wave's board. Recovery reads exactly two
// things off it: the conversation annotation and the turn's messages
// ([opmemory.ExtractTurnMessages] with the request annotation, which
// re-anchors the user message in its pre-inline form). Everything else —
// the world-state sections, the full-history replay prefix that
// replay_full_history puts on MainChannel, the board vars — is context
// for the model, not for recovery, and re-serializing it per wave is how
// one long turn ends up writing gigabytes.
//
// The trim is derived from the recovery path rather than from a second
// slicing rule: [opmemory.TurnSnapshot] runs the same extraction, and
// restoring the trimmed board and extracting again yields the same
// messages (the fixpoint the package tests pin).
func trimCheckpoint(ctx context.Context, cp agent.Checkpoint) agent.Checkpoint {
	trimmed, ok := opmemory.TurnSnapshot(cp.Board, checkpointRequest(ctx, &cp))
	if !ok {
		// No turn boundary on this board yet (a wave that has not
		// reached the world node). It carries almost nothing, so keeping
		// it costs nothing and recovery keeps its current behavior.
		return cp
	}
	if v, ok := cp.Board.Vars[boardVarThreadID]; ok {
		trimmed.Vars[boardVarThreadID] = v
	}
	cp.Board = trimmed
	return cp
}

// applyCheckpointTrim decides whether this configuration may persist
// trimmed wave checkpoints and installs (or clears) the store's hook. It
// runs at assembly and on every in-place reload, so a document that
// flips the decision takes effect with the generation that carries it.
//
// sessions.resume is the one setting that keeps whole boards: a resume
// seeds the turn from the checkpoint's board, where the trim keeps only
// what recovery reads. The deployments this repository ships leave it
// off, and turning it on trades the per-wave bytes back. An undecodable
// document leaves the store as it is; assembly fails on it separately.
func applyCheckpointTrim(
	ctx context.Context, store *sessions.Store, view *config.View,
) {
	if store == nil || view == nil {
		return
	}
	cfg, err := runtimecore.DecodeConfig(ctx, view.Document)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: decode runtime config for checkpoint trimming failed", err)
		return
	}
	if cfg.Sessions.Resume {
		store.SetCheckpointTrimmer(nil)
		return
	}
	store.SetCheckpointTrimmer(trimCheckpoint)
}

// dropRunCheckpoint removes one run's crash-recovery checkpoint once the
// turn it belongs to has a durable archive row. An unarchived run keeps
// its checkpoint: it is then the only trace of the turn, and the next
// assembly materializes it as an interrupted turn.
func (h *Host) dropRunCheckpoint(ctx context.Context, contextID, runID string) {
	if h == nil || h.store == nil || runID == "" {
		return
	}
	if contextID != "" {
		if _, _, err := h.store.State().ArchiveTurnByRun(
			ctx, contextID, runID,
		); err != nil {
			return
		}
	}
	telemetry.WarnErr(ctx, "host: delete run checkpoint failed",
		h.store.Delete(ctx, runID))
}

// deleteConversationCheckpoints removes the crash-recovery checkpoints
// left behind by one conversation. Deleting the conversation already
// drops its archive and memory rows; a checkpoint surviving that would
// let a later assembly resurrect it.
func (h *Host) deleteConversationCheckpoints(ctx context.Context, contextID string) {
	if h == nil || h.store == nil || contextID == "" {
		return
	}
	stateStore := h.store.State()
	checkpointIDs, err := stateStore.List(ctx)
	if err != nil {
		telemetry.WarnErr(ctx, "host: list run checkpoints failed", err)
		return
	}
	for _, id := range checkpointIDs {
		if !ids.IsRun(id) {
			continue
		}
		cp, err := stateStore.Load(ctx, id)
		if err != nil || cp == nil {
			continue
		}
		if checkpointConversationID(cp) != contextID {
			continue
		}
		telemetry.WarnErr(ctx, "host: delete conversation checkpoint failed",
			stateStore.Delete(ctx, id))
	}
}
