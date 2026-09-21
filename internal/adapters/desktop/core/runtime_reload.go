package core

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/httpprobe"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// RebuildRuntime invalidates pooled Hosts and immediately reassembles
// one for the active workspace when inference is configured. Bindings
// use this when the reload changes engine assembly inputs (plugin
// install/uninstall, workspace switch, startup). When the active
// workspace still has live runs the swap is deferred: the old runtime
// keeps serving until idle, then rebuilds in the background so no
// second Host ever serves the same workspace concurrently. The
// deferred rebuild is armed whenever Acquire hands out a retiring
// (stale) Host for the active workspace, including after a workspace
// switch away and back, so Runtime.current is never left pinned to a
// Host that closes itself once its live runs end.
func (c *Core) RebuildRuntime(ctx context.Context) error {
	c.reconcileProbe(ctx)
	oldHost := c.Runtime.Current()
	if err := c.Runtime.Reload(ctx); err != nil {
		return err
	}
	workDir := c.ActiveWorkDir()
	if strings.TrimSpace(workDir) == "" {
		c.EmitReady()
		return nil
	}
	mgr, err := config.Open(config.Options{UserDir: c.UserDir})
	if err != nil {
		return err
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return err
	}
	configured, err := config.RouterConfigured(view.Document)
	if err != nil {
		return err
	}
	if !configured {
		c.EmitReady()
		return nil
	}
	if _, err = c.Runtime.Acquire(ctx, workDir, interact.Auto{}); err != nil {
		return err
	}
	if h := c.Runtime.Current(); h != nil && h.IsStale() {
		// The active workspace's old runtime is still draining live
		// turns; rebuild once it is fully torn down. IsStale covers the
		// switch-away-and-back case where the draining Host was not the
		// previously current one, which used to leave the current Host
		// closed with no replacement scheduled.
		c.armRebuildAfterDrain(ctx, h, workDir)
		if oldHost == nil ||
			filepath.Clean(oldHost.WorkDir()) != filepath.Clean(workDir) {
			// A workspace switch landed on a draining Host: emit ready
			// now so the UI switches immediately; the deferred rebuild
			// emits again once the replacement Host is installed.
			c.EmitReady()
		}
		return nil
	}
	c.EmitReady()
	return nil
}

// armRebuildAfterDrain schedules the deferred replacement of a
// draining Host, at most one per workspace. A settings save, a
// workspace switch and a plugin write can all invalidate the same
// workspace inside one drain; without this guard every invalidation
// spawned its own watcher, and all of them assembled a replacement the
// moment the old Host closed — one runtime built and thrown away per
// waker. The single armed watcher always assembles from the document
// on disk at the time it runs, so a later invalidation needs no
// watcher of its own.
func (c *Core) armRebuildAfterDrain(
	ctx context.Context,
	old *host.Host,
	workDir string,
) {
	if !c.markRebuildPending(workDir) {
		return
	}
	go c.rebuildAfterDrain(context.WithoutCancel(ctx), old, workDir)
}

// markRebuildPending claims the armed-rebuild slot for one workspace
// and reports whether this caller owns it.
func (c *Core) markRebuildPending(workDir string) bool {
	key := filepath.Clean(workDir)
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()
	if _, ok := c.rebuildPending[key]; ok {
		return false
	}
	if c.rebuildPending == nil {
		c.rebuildPending = make(map[string]struct{})
	}
	c.rebuildPending[key] = struct{}{}
	return true
}

// clearRebuildPending releases the armed-rebuild slot, so a later
// reload can arm a new one.
func (c *Core) clearRebuildPending(workDir string) {
	key := filepath.Clean(workDir)
	c.rebuildMu.Lock()
	delete(c.rebuildPending, key)
	c.rebuildMu.Unlock()
}

// rebuildPendingFor reports whether a replacement is already armed for
// one workspace.
func (c *Core) rebuildPendingFor(workDir string) bool {
	key := filepath.Clean(workDir)
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()
	_, ok := c.rebuildPending[key]
	return ok
}

// ApplyDocumentReload applies document-only configuration changes
// (memory, inference/router, MCP, …) in place on the current Host via
// flowcraft's atomic generation swap, without tearing the runtime
// down. When no Host is current or the in-place reload cannot serve
// the document (validation failure, router unconfigured, sessions
// implementation change), it falls back to a full RebuildRuntime.
func (c *Core) ApplyDocumentReload(ctx context.Context) error {
	// MCP servers arrive in the document, and an in-place swap rebuilds
	// their clients, so the probe has to be reconciled before either
	// path reaches the runtime.
	c.reconcileProbe(ctx)
	h := c.Runtime.Current()
	if h == nil {
		return c.RebuildRuntime(ctx)
	}
	err := h.ReloadDocument(ctx)
	if err == nil {
		// The Host survives an in-place swap, but the document it serves
		// changed, and everything the UI reads out of that document is
		// now stale: the composer's model list and default reasoning
		// flag, the session defaults, the agent count. RebuildRuntime
		// emits ready for exactly this reason; the in-place path has to
		// signal the same way or the UI keeps the previous model list
		// after a settings save.
		c.EmitReady()
		return nil
	}
	// The fallback is a full rebuild, which is orders of magnitude more
	// expensive than the in-place swap. The reason the swap was refused
	// (router unconfigured, sessions implementation changed, validation
	// failure) used to be dropped on the floor, which left "why did a
	// settings save reassemble everything" unanswerable.
	telemetry.WarnErr(ctx,
		"host: in-place document reload unavailable; rebuilding",
		err,
		otellog.String("reason", string(host.AssemblyReasonFrom(ctx))),
		otellog.String("workspace", c.ActiveWorkDir()))
	return c.RebuildRuntime(ctx)
}

// HTTPProbeState is the diagnostics view of the provider round-trip
// probe: the persisted switch, the environment override, whether the
// wrapper is in place right now and, when it is not, the MCP
// configuration that keeps it out.
type HTTPProbeState struct {
	Enabled bool
	Env     bool
	Active  bool
	Blocker string
}

// HTTPProbeState reports how the probe is currently wired (Settings >
// Diagnostics > DEV tools).
func (c *Core) HTTPProbeState() HTTPProbeState {
	return HTTPProbeState{
		Enabled: c.Shell.HTTPProbe(),
		Env:     httpprobe.Enabled(),
		Active:  httpprobe.Active(),
		Blocker: probeBlocker(c.UserDir),
	}
}

// SetHTTPProbe persists the provider round-trip probe switch and applies
// it. Turning it on wraps the process transport and reloads the runtime,
// because the provider drivers read the transport when they build their
// clients; turning it off needs neither — the wrapper is what clients
// would be handed next and it checks the installed flag on every round
// trip, so a probe that already handed out transports goes quiet where it
// stands.
func (c *Core) SetHTTPProbe(ctx context.Context, enabled bool) error {
	if err := c.Shell.SetHTTPProbe(enabled); err != nil {
		return err
	}
	c.reconcileProbe(ctx)
	if !enabled || probeBlocker(c.UserDir) != "" {
		// Off is already effective, and a parked probe has nothing to
		// apply: a reload here would only churn the runtime.
		return nil
	}
	return c.ApplyDocumentReload(
		host.WithAssemblyReason(ctx, host.ReasonProbeSave))
}

// reconcileProbe keeps the provider round-trip probe (a diagnostic, see
// foundation/utils/httpprobe) in the state its opt-ins ask for: the
// switch (desktop.json diagnostics.httpProbe) or OPENCRAFT_HTTP_PROBE
// installs it, and neither counting as a yes removes it.
//
// A streamable-HTTP MCP client is built by flowcraft's
// core/utils.NewRoundTripper, which clones http.DefaultTransport through
// an unchecked *http.Transport assertion: with a wrapped process transport
// that assertion panics, and the client is built inside a reload. MCP
// servers are the only configuration that reaches that helper, so the
// probe stands down while one of them uses HTTP and returns when the last
// one is gone. Both directions happen before the runtime is touched, which
// is what makes the restored probe effective: the reload that follows
// assembles the provider clients, and they read the process transport as
// they are built.
func (c *Core) reconcileProbe(ctx context.Context) {
	if blocker := probeBlocker(c.UserDir); blocker != "" {
		if httpprobe.Uninstall() {
			telemetry.Warn(ctx, "httpprobe: probe stood down",
				otellog.String("reason", blocker))
		}
		return
	}
	if !c.Shell.HTTPProbe() && !httpprobe.Enabled() {
		if httpprobe.Uninstall() {
			telemetry.Info(ctx, "httpprobe: probe switched off")
		}
		return
	}
	if !httpprobe.Active() && httpprobe.Install() {
		telemetry.Info(ctx,
			"httpprobe: provider round trips are being recorded")
	}
}

// probeBlocker describes the MCP configuration that forces the probe to
// stand down, or "" when the process may keep a wrapped transport. An
// unreadable configuration counts as a blocker: this runs on the reload
// path, and standing down is the safe answer when the server list is
// unknown.
func probeBlocker(userDir string) string {
	servers, err := config.LoadMCP(userDir)
	if err != nil {
		return "the MCP configuration cannot be read"
	}
	for _, server := range servers {
		if strings.EqualFold(strings.TrimSpace(server.Transport), "http") {
			return "HTTP MCP server " + server.Name + " is configured"
		}
	}
	return ""
}

// rebuildAfterDrain waits for a stale Host to finish teardown, then
// acquires a fresh Host for the same workspace and signals readiness.
// Acquire itself never assembles a replacement while the old Host is
// still closing, so this is safe under any number of concurrent
// reloads.
func (c *Core) rebuildAfterDrain(
	ctx context.Context,
	old *host.Host,
	workDir string,
) {
	defer c.clearRebuildPending(workDir)
	if err := old.WaitClosed(ctx); err != nil {
		return
	}
	// The user may switch workspaces while the retired Host drains;
	// only reinstall a replacement when this workspace is still
	// active, so a background retire never hijacks Runtime.current for
	// a different workspace.
	if filepath.Clean(c.ActiveWorkDir()) != filepath.Clean(workDir) {
		return
	}
	ctx = host.WithAssemblyReason(ctx, host.ReasonRetryAfterDrain)
	if _, err := c.Runtime.Acquire(ctx, workDir, interact.Auto{}); err != nil {
		return
	}
	c.EmitReady()
}
