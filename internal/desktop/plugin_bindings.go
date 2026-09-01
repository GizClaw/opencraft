package desktop

// Thin wails bindings over internal/plugins. All plugin logic lives in
// the plugins package; these methods only connect it to the app state.

import (
	"errors"

	"github.com/GizClaw/opencraft/internal/plugins"
)

// PluginList returns every installed plugin.
func (a *App) PluginList() ([]plugins.PluginSummary, error) {
	if a.plugins == nil {
		return nil, errors.New("plugin store is not ready")
	}
	return a.plugins.List()
}

// PluginBundle returns the plugin entry bundle source.
func (a *App) PluginBundle(id string) (string, error) {
	if a.plugins == nil {
		return "", errors.New("plugin store is not ready")
	}
	return a.plugins.Bundle(id)
}

// PluginSetEnabled toggles a plugin's enabled state.
func (a *App) PluginSetEnabled(id string, enabled bool) error {
	if a.plugins == nil {
		return errors.New("plugin store is not ready")
	}
	if err := a.plugins.SetEnabled(id, enabled); err != nil {
		return err
	}
	if !enabled && a.cap != nil {
		a.cap.Stop(id)
	}
	// Agent-facing capabilities (skills / MCP / hooks / tools) are
	// assembled into the runtime; refresh it so the change takes
	// effect (immediately, or after the current turn when one runs).
	return a.refreshAgentPlugins()
}

// PluginInstall copies a plugin folder into the plugin root.
func (a *App) PluginInstall(src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.Install(src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginInstallZip installs a plugin from a zip package (a release
// artifact). The archive is extracted safely and installed through the
// normal Install path.
func (a *App) PluginInstallZip(zipPath string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.InstallZip(zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginUninstall removes a plugin and its KV data.
func (a *App) PluginUninstall(id string) error {
	if a.plugins == nil {
		return errors.New("plugin store is not ready")
	}
	// Ask the capability plugin to clean up its own resources first
	// (inference profile, secrets): the plugin knows what it wrote.
	if a.cap != nil {
		// Best-effort: the host fallback below still removes leftovers.
		_ = a.cap.Cleanup(id)
	}
	// Host fallback: a plugin may not implement cleanup or may have
	// written resources earlier; remove lingering inference config and
	// secrets defensively so nothing survives the uninstall.
	profileRemoved := false
	if err := a.removeInferenceProfile(id); err == nil {
		profileRemoved = true
	}
	if a.secrets != nil {
		_ = a.secrets.DeletePrefix(a.appContext(), "auth/"+id+"/")
	}
	a.kv.RemoveAll(id)
	if err := a.plugins.Uninstall(id); err != nil {
		return err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	// Notify before rebuild so the settings page reflects the config
	// change even if the rebuild fails.
	if profileRemoved && a.bridge != nil {
		a.bridge.Emit("inference_changed", map[string]any{})
	}
	return a.refreshAgentPlugins()
}
