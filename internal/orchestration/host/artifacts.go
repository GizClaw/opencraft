package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
)

// BufferObservedArtifact persists one workspace write into the owning
// conversation's artifact buffer, attributed to the run that wrote it.
//
// Delegated subagent runs mint an ephemeral "ctx-" conversation id that the
// session store rejects. Their fold and their writes are not persisted, so
// buffering is skipped exactly like the committer and the archive observer
// skip it — without the guard every file a subagent writes logs a rejected
// store call.
func BufferObservedArtifact(
	store *ocsessions.Store,
	ctx context.Context,
	path string,
	data []byte,
) {
	conversationID, runID := artifactOwner(ctx)
	if conversationID == "" || runID == "" || store == nil {
		return
	}
	telemetry.WarnErr(ctx, "host: buffer observed artifact failed",
		store.BufferArtifact(conversationID, runID, path, len(data)),
		otellog.String("conversation.id", conversationID),
		otellog.String("run.id", runID),
		otellog.String("path", path))
}

// artifactOwner returns the conversation and the run that buffer
// artifacts for the run in ctx, or two empty strings when the write has
// no turn of its own: no engine run info, an ephemeral id the session
// store cannot own, or a run the engine never identified. The run id is
// part of the answer because the buffer is keyed by it — the turn that
// archives first must not absorb what another run wrote.
func artifactOwner(ctx context.Context) (conversationID, runID string) {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || !ids.IsSession(info.ConversationID) || info.RunID == "" {
		return "", ""
	}
	return info.ConversationID, info.RunID
}

// onArtifactWrite notifies the external observer and buffers the write
// for the turn's archive.
//
// This write path is the only source of turn artifacts, and deliberately
// the only one: nothing walks the workspace around a turn to reconcile
// what changed, so a file written outside this path (a script, a bash
// command, an MCP server) is not an artifact until an event stream can
// attribute the write to a turn. ArchiveTurn flushes the buffer when the
// turn is archived.
func (h *Host) onArtifactWrite(ctx context.Context, path string, data []byte) {
	h.mu.Lock()
	fn := h.artifact
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, path, data)
	}
	BufferObservedArtifact(h.store, ctx, path, data)
}
