package desktop

// Thin wails bindings over internal/plugins. All plugin logic lives in
// the plugins package; these methods only connect it to the app state.

import (
	"errors"

	"github.com/GizClaw/opencraft/internal/plugins"
	pluginupdate "github.com/GizClaw/opencraft/internal/plugins/update"
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

// PluginInspect reads a plugin source folder/zip and reports its
// manifest summary (including whether it would shadow a builtin)
// without installing it. The install dialog uses it for pre-flight
// warnings.
func (a *App) PluginInspect(src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	return a.plugins.Inspect(src)
}

// PluginUpdate replaces an installed plugin with a newer version from
// a local directory. The previous version is kept for rollback.
func (a *App) PluginUpdate(id, src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.Update(id, src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginUpdateZip updates a plugin from a zip package.
func (a *App) PluginUpdateZip(id, zipPath string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.UpdateZip(id, zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginRollback restores the previous version snapshot of a plugin.
func (a *App) PluginRollback(id string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.Rollback(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginCheckUpdate fetches the plugin's declared update manifest and
// returns the newest version metadata without applying anything.
func (a *App) PluginCheckUpdate(id string) (plugins.UpdateInfo, error) {
	if a.plugins == nil {
		return plugins.UpdateInfo{}, errors.New("plugin store is not ready")
	}
	m, err := a.plugins.Manifest(id)
	if err != nil {
		return plugins.UpdateInfo{}, err
	}
	if m.Update == nil {
		return plugins.UpdateInfo{}, errors.New(
			"plugin does not declare an update url")
	}
	return pluginupdate.Check(a.appContext(), m.Update.URL)
}

// PluginApplyUpdate checks the update manifest, downloads and verifies
// the package, then applies it through the normal update pipeline
// (version constraint, resource validation, rollback snapshot).
func (a *App) PluginApplyUpdate(id string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	m, err := a.plugins.Manifest(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if m.Update == nil {
		return plugins.PluginSummary{}, errors.New(
			"plugin does not declare an update url")
	}
	info, err := pluginupdate.Check(a.appContext(), m.Update.URL)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	zipPath, cleanup, err := pluginupdate.FetchZip(
		a.appContext(), info, pluginupdate.Policy{})
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	defer cleanup()
	_, builtin, err := a.plugins.Dir(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	var sum plugins.PluginSummary
	if builtin {
		// The builtin bundle is read-only: applying a remote update
		// installs the package as a user-root shadow copy. The builtin
		// stays intact and reappears if the shadow is uninstalled.
		sum, err = a.plugins.InstallZip(zipPath)
	} else {
		sum, err = a.plugins.UpdateZip(id, zipPath)
	}
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
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
