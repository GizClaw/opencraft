package core

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/httpprobe"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// RebuildRuntime invalidates every pooled Host and immediately
// reassembles the active workspace's one when inference is configured.
// Bindings use this when the reload changes engine assembly inputs
// (plugin install/uninstall, workspace switch, startup).
//
// A workspace whose old runtime still has live runs cannot be
// reassembled yet — a second Host would serve the same conversations
// concurrently — so that swap is deferred: the retirement, the wait for
// the drain and the assembly of the replacement belong to the pool
// (host.Manager.ScheduleReplacement), which is the only layer that
// knows who is draining and who has already retired. This method only
// decides what the UI has to hear.
func (c *Core) RebuildRuntime(ctx context.Context) error {
	c.reconcileProbe(ctx)
	announced := c.readyWorkspace()
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
	h, err := c.Runtime.EnsureHost(ctx, workDir)
	if err != nil {
		return err
	}
	if h.IsStale() {
		// The active workspace is still running on the assembly this
		// reload retired. The pool assembles its replacement as soon as
		// the drain ends and announces it through EmitReady; arming is
		// once per workspace, so a reload storm inside one drain asks
		// for one replacement.
		c.Runtime.ScheduleReplacement(ctx, workDir)
		if !SameWorkspace(announced, workDir) {
			// A switch away and back lands on a Host that was retired
			// while the window was elsewhere: that Host has no
			// replacement scheduled (leaving a workspace does not
			// rebuild it), and the UI switches workspaces on ready
			// alone — it cannot wait out a drain that may last a whole
			// turn. Announce the switch now; the replacement announces
			// itself again when it lands.
			c.EmitReady()
		}
		return nil
	}
	c.EmitReady()
	return nil
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
	h := c.ActiveHost()
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
