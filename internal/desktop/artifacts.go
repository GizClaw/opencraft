package desktop

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// bufferObservedArtifact persists one workspace write into the owning
// conversation's artifact buffer. The main runtime also emits the
// matching "artifact" UI event before buffering; background hosts only
// buffer because their runs are not surfaced as live chat turns.
func bufferObservedArtifact(
	store *ocsessions.Store,
	ctx context.Context,
	path string,
	data []byte,
) {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || info.ConversationID == "" || store == nil {
		return
	}
	_ = store.BufferArtifact(info.ConversationID, path, len(data))
}

// onArtifactWrite re-emits one observed workspace write as an
// "artifact" UI event attributed to the owning conversation. Only
// writes made inside a session run reach this point (observingWorkspace
// filters by RunInfo), so the frontend can count per-turn outputs
// without counting engine-internal writes.
func (a *App) onArtifactWrite(ctx context.Context, path string, data []byte) {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || info.ConversationID == "" {
		return
	}
	a.bridge.Emit("artifact", map[string]any{
		"conversation_id": info.ConversationID,
		"path":            path,
		"bytes":           len(data),
	})
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	bufferObservedArtifact(store, ctx, path, data)
}
