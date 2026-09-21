package core

import (
	"path/filepath"
	"sync"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// pluginInferenceWrite remembers how the last runtime rebuild triggered
// by a capability-plugin inference write went. A plugin re-submits its
// whole row set on every catalog sync, so a write that leaves the stored
// rows identical does not need a rebuild — the live runtime already
// serves that document. After a failed rebuild the rows may have changed
// without reaching the runtime, so the next write rebuilds again and
// returns the error to the plugin, which is how a row that cannot deploy
// (an endpoint fact mismatch, say) is reported and retried.
type pluginInferenceWrite struct {
	mu sync.Mutex
	// applied is true when the last rebuild requested by this path
	// succeeded. The zero value is false, so the first no-op write of a
	// process still rebuilds once instead of assuming a good runtime.
	applied bool
}

// markApplied records the outcome of a rebuild triggered by a plugin
// inference write.
func (w *pluginInferenceWrite) markApplied(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applied = err == nil
}

// lastRebuildApplied reports whether the last rebuild requested by a
// plugin inference write succeeded.
func (w *pluginInferenceWrite) lastRebuildApplied() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.applied
}

// wirePluginInference routes capability-plugin inference profile
// primitives into the user config. The row shape, the source policy
// (identity, credential namespace, the user-owned enabled flag) and the
// ownership sidecar all live in config; this wiring only reports the
// change and rebuilds the runtime so the new deployment takes effect.
func (c *Core) wirePluginInference() {
	if c.Plugin == nil || c.Plugin.Capability == nil {
		return
	}
	c.Plugin.Capability.SetInferenceHandler(pluginruntime.InferenceHandler{
		Upsert: func(pluginID string, profile pluginruntime.InferenceProfile) error {
			changed, err := config.UpsertPluginInstance(
				c.UserDir, pluginID, profile,
			)
			if err != nil {
				return err
			}
			return c.applyPluginInferenceWrite(changed)
		},
		Remove: func(pluginID, id string) error {
			changed, err := config.RemovePluginInstance(c.UserDir, pluginID, id)
			if err != nil {
				return err
			}
			return c.applyPluginInferenceWrite(changed)
		},
	})
}

// applyPluginInferenceWrite rebuilds the runtime for a write that
// changed the stored rows. An identical row is applied to a live
// runtime only when the previous rebuild succeeded and a current Host
// still serves the active workspace; otherwise the rebuild runs so its
// error reaches the plugin.
func (c *Core) applyPluginInferenceWrite(changed bool) error {
	if changed {
		c.Shell.Emit("inference_changed", map[string]any{})
	} else if c.pluginWrites.lastRebuildApplied() &&
		c.runtimeServesActiveWorkspace() {
		return nil
	}
	ctx := host.WithAssemblyReason(
		c.Shell.Context(), host.ReasonInferenceChange)
	err := c.RebuildRuntime(ctx)
	c.pluginWrites.markApplied(err)
	return err
}

// runtimeServesActiveWorkspace reports whether a live Host already
// serves the active workspace, i.e. whether the assembled runtime in
// use was built from the document on disk.
func (c *Core) runtimeServesActiveWorkspace() bool {
	h := c.Runtime.Current()
	if h == nil || h.IsStale() || h.IsClosing() {
		return false
	}
	active := c.ActiveWorkDir()
	if active == "" {
		return true
	}
	return filepath.Clean(h.WorkDir()) == filepath.Clean(active)
}

// RemovePluginInference removes every inference deployment owned by
// pluginID and reports whether any deployment was removed. It is the
// host-side fallback used when a plugin is disabled or uninstalled;
// secrets are not touched here.
func (c *Core) RemovePluginInference(pluginID string) (bool, error) {
	if err := plugins.ValidateID(pluginID); err != nil {
		return false, err
	}
	return config.RemovePluginInstances(c.UserDir, pluginID)
}
