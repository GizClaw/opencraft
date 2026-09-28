package core

import (
	"context"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// RefreshPluginRuntime reassembles the runtime from the plugin
// registry, once per registry revision.
//
// The revision is the register clock (see capabilities/plugins'
// charter, FaceRefreshes): every successful registry mutation moves it,
// and this method rebuilds only while the pooled runtimes are behind
// it. The lock serializes the rebuilds, so a burst — an install, a
// disable and a rollback racing each other — costs one assembly for
// everything that landed while one already ran, instead of one
// assembly per caller. A rebuild that ends up covering no new revision
// does nothing at all.
//
// Both paths that mutate the registry go through here: the settings
// page's binding and the agent-authored install, so neither can stack a
// redundant rebuild on top of the other.
func (c *Core) RefreshPluginRuntime(ctx context.Context) error {
	c.pluginRefreshMu.Lock()
	defer c.pluginRefreshMu.Unlock()
	rev := c.pluginRegistryRevision()
	for rev != c.pluginRefreshRev {
		rebuildCtx := host.WithAssemblyReason(ctx, host.ReasonPluginChange)
		if err := c.RebuildRuntime(rebuildCtx); err != nil {
			return err
		}
		c.pluginRefreshRev = rev
		// A mutation that landed while the rebuild ran rides this
		// pass: re-read the clock and, if it moved, rebuild again
		// here, so the caller of the last mutation in a burst does
		// not have to.
		rev = c.pluginRegistryRevision()
	}
	return nil
}

// pluginRegistryRevision reads the plugin registry clock, 0 when this
// core has no registry.
func (c *Core) pluginRegistryRevision() uint64 {
	if c.Plugin == nil || c.Plugin.Store == nil {
		return 0
	}
	return c.Plugin.Store.Revision()
}
