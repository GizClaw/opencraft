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
func BufferObservedArtifact(
	store *ocsessions.Store,
	ctx context.Context,
	path string,
	data []byte,
) {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || info.ConversationID == "" || store == nil {
		return
	}
	telemetry.WarnErr(ctx, "host: buffer observed artifact failed",
		store.BufferArtifact(info.ConversationID, path, len(data)),
		otellog.String("conversation.id", info.ConversationID),
		otellog.String("path", path))
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
