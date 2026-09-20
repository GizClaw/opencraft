package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// BufferObservedArtifact persists one workspace write into the owning
// conversation's artifact buffer.
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
	id := artifactConversation(ctx)
	if id == "" || store == nil {
		return
	}
	telemetry.WarnErr(ctx, "host: buffer observed artifact failed",
		store.BufferArtifact(id, path, len(data)),
		otellog.String("conversation.id", id),
		otellog.String("path", path))
}

// artifactConversation returns the conversation that buffers artifacts for
// the run in ctx, or "" when the run has none: no engine run info, an empty
// id, or an ephemeral id the session store cannot own.
func artifactConversation(ctx context.Context) string {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || !ocsessions.ValidID(info.ConversationID) {
		return ""
	}
	return info.ConversationID
}

// onArtifactWrite notifies the external observer and buffers the write
// for the turn's archive.
func (h *Host) onArtifactWrite(ctx context.Context, path string, data []byte) {
	h.mu.Lock()
	fn := h.artifact
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, path, data)
	}
	BufferObservedArtifact(h.store, ctx, path, data)
}
