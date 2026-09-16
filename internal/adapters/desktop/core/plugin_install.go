package core

import (
	"context"
	"fmt"
	"os"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	plugininstalltool "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
)

// pluginInstaller adapts the desktop plugin registry to the agent's
// plugin authoring tools (plugin_install / plugin_update /
// plugin_list). The registry root lives under the user data dir, so
// the copy and the manifest validation run here, on the host, and
// every mutation reloads the runtime so the new plugin's skills,
// tools, MCP servers and hooks reach the next assembly.
type pluginInstaller struct {
	core *Core
}

// NewPluginInstaller returns the agent-facing installer over the
// desktop plugin registry.
func NewPluginInstaller(c *Core) plugininstalltool.Installer {
	return pluginInstaller{core: c}
}

// Empty reports that this runtime has no plugin registry, which keeps
// the plugin authoring tools out of the catalog entirely.
func (p pluginInstaller) Empty() bool {
	return p.core == nil || p.core.Plugin == nil || p.core.Plugin.Store == nil
}

// PluginsList returns every installed plugin.
func (p pluginInstaller) PluginsList(
	_ context.Context,
) ([]plugins.PluginSummary, error) {
	return p.core.Plugin.Store.List()
}

// PluginInspect reads a source manifest without installing it.
func (p pluginInstaller) PluginInspect(
	_ context.Context, src string,
) (plugins.PluginSummary, error) {
	return p.core.Plugin.Store.Inspect(src)
}

// PluginInstall copies a source into the registry and reloads the
// runtime. The reload is deferred while the calling turn still runs,
// so the install never tears down the run that requested it.
func (p pluginInstaller) PluginInstall(
	ctx context.Context, src string,
) (plugins.PluginSummary, error) {
	sum, err := p.installSource(src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, p.reload(ctx)
}

// PluginUpdate replaces an installed plugin with a newer source and
// reloads the runtime, stopping the previous capability process first
// (its binary is replaced on disk).
func (p pluginInstaller) PluginUpdate(
	ctx context.Context, id, src string,
) (plugins.PluginSummary, error) {
	isDir, err := sourceIsDir(src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	var sum plugins.PluginSummary
	if isDir {
		sum, err = p.core.Plugin.Store.Update(id, src)
	} else {
		sum, err = p.core.Plugin.Store.UpdateZip(id, src)
	}
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	p.core.Plugin.Capability.Stop(id)
	return sum, p.reload(ctx)
}

// installSource installs one source: a directory containing plugin.json
// (Install) or a package file (InstallZip). The settings page draws the
// same line, and the registry validates the archive (zip-slip, size
// bounds, manifest) before anything lands in the registry.
func (p pluginInstaller) installSource(src string) (plugins.PluginSummary, error) {
	isDir, err := sourceIsDir(src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if isDir {
		return p.core.Plugin.Store.Install(src)
	}
	return p.core.Plugin.Store.InstallZip(src)
}

// sourceIsDir reports whether a plugin source is a directory rather
// than a package file.
func sourceIsDir(src string) (bool, error) {
	info, err := os.Stat(src)
	if err != nil {
		return false, fmt.Errorf("plugins: source: %w", err)
	}
	return info.IsDir(), nil
}

// reload reassembles the runtime from the plugin registry change. It
// mirrors the plugin binding's refresh, so an install from the agent
// and an install from the settings page take the same path — including
// running on the shell's app context rather than the calling turn's:
// the registry change is already durable, so pressing stop in the same
// instant must not leave the runtime unassembled.
func (p pluginInstaller) reload(ctx context.Context) error {
	if p.core.Shell != nil {
		ctx = p.core.Shell.Context()
	}
	return p.core.RebuildRuntime(ctx)
}

var _ plugininstalltool.Installer = pluginInstaller{}
