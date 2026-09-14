package core

import (
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

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
			if err := config.UpsertPluginInstance(c.UserDir, pluginID, profile); err != nil {
				return err
			}
			c.Shell.Emit("inference_changed", map[string]any{})
			return c.RebuildRuntime(c.Shell.Context())
		},
		Remove: func(pluginID, id string) error {
			if err := config.RemovePluginInstance(c.UserDir, pluginID, id); err != nil {
				return err
			}
			c.Shell.Emit("inference_changed", map[string]any{})
			return c.RebuildRuntime(c.Shell.Context())
		},
	})
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
