package desktop

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
)

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
	// Buffer the artifact into the session store so the commit/archive
	// hook persists it with the turn file ("artifacts" field); resume
	// then replays one strip per turn.
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	if store != nil {
		_ = store.BufferArtifact(info.ConversationID, path, len(data))
	}
}
