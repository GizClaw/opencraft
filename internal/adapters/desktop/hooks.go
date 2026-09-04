package desktop

import "context"

// fireHooks forwards one adapter-owned lifecycle event (session start,
// session end, workspace transitions) through the current Host, which
// owns the runtime's hooks manager. Payload content is never logged by
// the hooks package; hook execution failures surface through telemetry
// without blocking the UI.
func (a *App) fireHooks(ctx context.Context, event string, payload map[string]any) {
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h == nil {
		return
	}
	h.FireHook(ctx, event, payload)
}
