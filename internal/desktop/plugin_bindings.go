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
	return nil
}

// PluginInstall copies a plugin folder into the plugin root.
func (a *App) PluginInstall(src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	return a.plugins.Install(src)
}

// PluginUninstall removes a plugin and its KV data.
func (a *App) PluginUninstall(id string) error {
	if a.plugins == nil {
		return errors.New("plugin store is not ready")
	}
	// Ask the capability plugin to clean up its own resources first
	// (inference profile, secrets): the plugin knows what it wrote.
	if a.cap != nil {
		a.cap.Cleanup(id)
	}
	// Host fallback: a plugin may not implement cleanup or may have
	// written resources earlier; remove lingering inference config and
	// secrets defensively so nothing survives the uninstall.
	if err := a.removeInferenceProfile(id); err == nil {
		_ = a.rebuild()
		if a.bridge != nil {
			a.bridge.Emit("inference_changed", map[string]any{})
		}
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
	return nil
}
