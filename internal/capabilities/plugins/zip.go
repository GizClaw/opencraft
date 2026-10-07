package plugins

import (
	"github.com/GizClaw/opencraft/internal/foundation/utils/zipx"
)

// InstallZip installs a plugin from a zip package (e.g. a GitHub
// release artifact). The archive may carry the plugin files at its
// root or under a single top-level directory; plugin.json is located
// and that folder is installed through the normal Install path
// (manifest validation, exec bit, ad-hoc signing).
func (s *Store) InstallZip(zipPath string) (PluginSummary, error) {
	dir, cleanup, err := zipx.Extract(zipPath, pluginJSON)
	if err != nil {
		return PluginSummary{}, err
	}
	defer cleanup()
	return s.Install(dir)
}
