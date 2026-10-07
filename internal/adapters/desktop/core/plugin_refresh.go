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
//
// The rebuild is the both-scopes one (RebuildRuntimeAll): a plugin's
// providers land in the user's inference wiring, which a workspace reads
// through its document and an application through the overlay over its
// layers. A rebuild that stopped at the workspace scope would leave every
// installed application serving the provider set from before the
// mutation.
//
// The trailing passes are capped at two: the rebuild that covers the
// revision the caller saw, and one that folds in whatever landed while
// it ran. Without the cap a registry under a continuous stream of
// mutations — an agent installing plugins in a loop, a settings page
// being clicked through — turns this loop into one full assembly per
// mutation for as long as the stream lasts. What the loop owes a caller
// is that the revision read before the call is covered; anything that
// lands after the second pass is the next caller's work, and every
// mutation path calls here.
func (c *Core) RefreshPluginRuntime(ctx context.Context) error {
	c.pluginRefreshMu.Lock()
	defer c.pluginRefreshMu.Unlock()
	rev := c.pluginRegistryRevision()
	for pass := 0; pass < maxPluginRefreshPasses && rev != c.pluginRefreshRev; pass++ {
		rebuildCtx := host.WithAssemblyReason(ctx, host.ReasonPluginChange)
		if err := c.RebuildRuntimeAll(rebuildCtx); err != nil {
			return err
		}
		c.pluginRefreshRev = rev
		// A mutation that landed while the rebuild ran rides this
		// pass: re-read the clock and, if it moved, rebuild again
		// here, so the caller of the last mutation in a burst does
		// not have to. A revision still ahead after the capped pass
		// stays behind on purpose (see above): this call returns with
		// the runtime one revision back, and the next caller — every
		// mutation path calls here — picks the rest up.
		rev = c.pluginRegistryRevision()
	}
	return nil
}

// maxPluginRefreshPasses bounds one RefreshPluginRuntime call: the pass
// that covers the caller's revision, plus one trailing pass.
const maxPluginRefreshPasses = 2

// pluginRegistryRevision reads the plugin registry clock, 0 when this
// core has no registry.
func (c *Core) pluginRegistryRevision() uint64 {
	if c.Plugin == nil || c.Plugin.Store == nil {
		return 0
	}
	return c.Plugin.Store.Revision()
}
