package core

import (
	"context"
	"strings"

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
// second Host ever serves the same workspace concurrently.
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
	if oldHost != nil && c.Runtime.Current() == oldHost {
		// The old runtime is still draining live turns; rebuild once
		// it is fully torn down.
		go c.rebuildAfterDrain(
			context.WithoutCancel(ctx), oldHost, workDir)
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
	if err := h.ReloadDocument(ctx); err == nil {
		return nil
	}
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
	if _, err := c.Runtime.Acquire(ctx, workDir, interact.Auto{}); err != nil {
		return
	}
	c.EmitReady()
}
