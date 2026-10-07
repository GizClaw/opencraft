package core

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// ConfigureHost wires the window's per-Host observers: the events one
// assembly reports about its own work, named with the scope they belong
// to. The pool applies it once per Host it hands out (see
// host.Manager.SetHostConfigurator), on every delivery path, so a Host
// that reaches the UI never stays silent.
//
// It is the adapter half of "who is this event for": a Host reports the
// files its turns write and the conversations it changed, and this is
// where that becomes a UI event. Both observers name their Host's
// application (empty = a workspace) so the window can route by scope
// instead of guessing from an id the two scopes share a generator for.
func (c *Core) ConfigureHost(h *host.Host) {
	if h == nil {
		return
	}
	h.SetArtifactObserver(func(ctx context.Context, path string, data []byte) {
		if !c.hostSpeaks(h) {
			return
		}
		info, ok := agent.RunInfoFromContext(ctx)
		if !ok || info.ConversationID == "" {
			return
		}
		c.Shell.Emit(EventArtifact, ArtifactEvent{
			AppID:          h.AppID(),
			ConversationID: info.ConversationID,
			RunID:          info.RunID,
			Path:           path,
			Bytes:          len(data),
		})
	})
	h.SetSessionUpdated(func(_ context.Context, contextID string) {
		if !c.hostSpeaks(h) {
			return
		}
		c.Shell.Emit(EventSessionUpdated, SessionUpdatedEvent{
			AppID: h.AppID(),
			ID:    contextID,
		})
	})
}

// hostSpeaks reports whether one Host's observer events are for what the
// window is showing. A workspace Host speaks only while it is the one
// serving the window's workspace: a Host the user has left (or one that
// already retired, while the replacement assembles) must not append rows
// to a transcript the window has moved away from. An application Host
// always speaks: it is nobody's "current" workspace, its page exists
// exactly as long as the application is open, and dropping its events
// because some other workspace happens to be on screen would leave the
// page half-rendered.
func (c *Core) hostSpeaks(h *host.Host) bool {
	if h == nil {
		return false
	}
	if h.AppID() != "" {
		return true
	}
	return h == c.ActiveHost()
}
