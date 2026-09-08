package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/GizClaw/flowcraft/core/event"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
)

// ReloadDocument applies document-only configuration changes (memory,
// inference/router, MCP, …) to this Host in place: it loads the same
// merged document used by assembly, validates it, and swaps the
// runtime generation atomically. In-flight turns finish on the old
// generation and the next Start uses the new one. Per-generation Host
// bindings are rebound synchronously before the call returns, and a
// document that swaps the sessions implementation is rejected so the
// adapter can fall back to a full rebuild while the reload entry point
// is still on the call stack. Callers that need a full rebuild (plugin
// installs, engine-input changes) must use the Manager path instead.
func (h *Host) ReloadDocument(ctx context.Context) error {
	if h == nil || h.ctrl == nil {
		return ErrRuntimeNotReady
	}
	doc, err := engine.LoadDocument(ctx, h.userDir)
	if err != nil {
		return fmt.Errorf("host: load document: %w", err)
	}
	configured, err := config.RouterConfigured(doc)
	if err != nil {
		return fmt.Errorf("host: check router configuration: %w", err)
	}
	if !configured {
		return errors.New("host: inference router is not configured")
	}
	result, err := h.ctrl.Reload(ctx, doc)
	if err != nil {
		return fmt.Errorf("host: in-place document reload: %w", err)
	}
	if !h.sessionsStoreMatches() {
		telemetry.Error(ctx,
			"host: sessions.Store identity changed across runtime reload; full rebuild required",
			otellog.Int64("runtime.generation.id", int64(result.GenerationID)))
		return errors.New(
			"host: in-place document reload changed the sessions implementation; full rebuild required")
	}
	// Rebind the new generation's per-generation resources before the
	// reload returns so no later turn can run against stale host
	// bindings. The runtime event router also dispatches this work
	// asynchronously for reloads initiated outside ReloadDocument;
	// onRuntimeReload serializes both paths and is idempotent.
	h.onRuntimeReload(context.Background(),
		runtimecore.RuntimeRebuildEvent{GenerationID: result.GenerationID})
	return nil
}

// attachRuntimeReloadObserver subscribes the Host to flowcraft's
// rebuild-completed lifecycle event. Every in-place Runtime.Reload
// publishes the event on the new generation's bus; the router forwards
// it to external attachments (verified in M0), so Host bindings follow
// the current generation no matter who initiated the reload.
func (h *Host) attachRuntimeReloadObserver(ctx context.Context) {
	rt := h.Controller().Runtime()
	if rt == nil {
		return
	}
	_, err := rt.Attach(
		ctx,
		event.Pattern(runtimecore.SubjectRuntimeRebuildCompleted()),
		event.SinkFunc(func(_ context.Context, env event.Envelope) error {
			var ev runtimecore.RuntimeRebuildEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				telemetry.WarnErr(
					context.Background(),
					"host: decode runtime rebuild event failed", err)
				return nil
			}
			h.onRuntimeReload(context.Background(), ev)
			return nil
		}),
	)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: attach runtime reload observer failed", err)
	}
	// The attachment lives as long as the runtime: Runtime.Attach tears
	// every subscription down when the runtime closes, so no detach
	// bookkeeping is needed here.
}

// onRuntimeReload rebinds every Host resource that is a per-generation
// instance and refreshes cached fields after an in-place reload. It
// runs once per RebuildCompleted event and once synchronously inside
// ReloadDocument; rebindMu keeps concurrent runs from interleaving
// resource Bind/LoadAll side effects.
func (h *Host) onRuntimeReload(
	ctx context.Context,
	_ runtimecore.RuntimeRebuildEvent,
) {
	h.rebindMu.Lock()
	defer h.rebindMu.Unlock()
	h.rebindAgents(ctx)
	h.refreshHooks(ctx)
	h.rebindArtifactObserver()
}

// rebindAgents binds the new generation's agentlifecycle resource and
// reloads persisted dynamic-agent declarations, mirroring the assembly
// tail in Manager.assemble.
func (h *Host) rebindAgents(ctx context.Context) {
	rt := h.Controller().Runtime()
	if rt == nil {
		return
	}
	var lifecycle *ocsagents.Lifecycle
	if value, ok := rt.Resource("agentlifecycle"); ok {
		if lc, ok := value.(*ocsagents.Lifecycle); ok && lc != nil {
			lifecycle = lc
			lifecycle.Bind(rt)
			for _, failure := range lifecycle.LoadAll(ctx) {
				telemetry.Warn(ctx,
					"host: reload agent declaration failed",
					otellog.String("agent", failure.Name),
					otellog.String("error", failure.Err.Error()))
			}
		}
	}
	h.agents.Store(lifecycle)
	if lifecycle == nil {
		telemetry.Warn(ctx,
			"host: agentlifecycle resource missing after runtime reload")
	}
}

// refreshHooks points the Host's cached hooks manager at the new
// generation's instance.
func (h *Host) refreshHooks(ctx context.Context) {
	rt := h.Controller().Runtime()
	if rt == nil {
		return
	}
	var mgr *hooks.Manager
	if value, ok := rt.Resource("hooks"); ok {
		if m, ok := value.(*hooks.Manager); ok && m != nil {
			mgr = m
		}
	}
	h.hooks.Store(mgr)
	if mgr == nil {
		telemetry.Warn(ctx,
			"host: hooks resource missing after runtime reload")
	}
}

// rebindArtifactObserver re-installs the Host's artifact sink on the
// new generation's artifacts resource.
func (h *Host) rebindArtifactObserver() {
	rt := h.Controller().Runtime()
	if rt == nil {
		return
	}
	if value, ok := rt.Resource("artifacts"); ok {
		if obs, ok := value.(*sandbox.ArtifactObserver); ok && obs != nil {
			obs.SetSink(h.onArtifactWrite)
		}
	}
}

// sessionsStoreMatches enforces the D3 invariant: after an in-place
// reload the resolved sessions.Store must be the same shared object
// the Host persists to. A document that swaps the sessions
// implementation cannot be served in-place. ReloadDocument checks this
// synchronously right after the swap and reports the requirement for a
// full rebuild to the caller, so the adapter fallback reassembles a
// fresh Host instead of leaving the pool current pinned to one that is
// retiring.
func (h *Host) sessionsStoreMatches() bool {
	rt := h.Controller().Runtime()
	if rt == nil {
		return false
	}
	value, ok := rt.Resource("sessions")
	store, ok2 := value.(*ocsessions.Store)
	return ok && ok2 && store != nil && store == h.store
}
