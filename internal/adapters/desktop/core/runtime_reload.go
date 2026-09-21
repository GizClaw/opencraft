package core

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/config"
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
		go c.rebuildAfterDrain(
			context.WithoutCancel(ctx), h, workDir)
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

// ApplyDocumentReload applies document-only configuration changes
// (memory, inference/router, MCP, …) in place on the current Host via
// flowcraft's atomic generation swap, without tearing the runtime
// down. When no Host is current or the in-place reload cannot serve
// the document (validation failure, router unconfigured, sessions
// implementation change), it falls back to a full RebuildRuntime.
func (c *Core) ApplyDocumentReload(ctx context.Context) error {
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
