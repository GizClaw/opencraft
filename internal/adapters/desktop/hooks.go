package desktop

import (
	"context"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
)

// fireHooks resolves the hooks manager from the runtime and fires one
// lifecycle event. Payload content is never logged by the hooks
// package; hook execution failures surface through telemetry without
// blocking the agent loop.
func (a *App) fireHooks(ctx context.Context, event string, payload map[string]any) {
	ctrl := a.controller()
	if ctrl == nil || ctrl.Runtime() == nil {
		return
	}
	value, ok := ctrl.Runtime().Resource("hooks")
	if !ok {
		return
	}
	mgr, ok := value.(*hooks.Manager)
	if !ok || mgr == nil || mgr.Empty() {
		return
	}
	mgr.Fire(ctx, event, payload)
}
