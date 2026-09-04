package bindings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginupdate "github.com/GizClaw/opencraft/internal/capabilities/plugins/update"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
)

// Plugin exposes plugin registry, KV and capability invocation.
type Plugin struct {
	core *core.Core
}

// NewPluginBinding wires the plugin binding.
func NewPluginBinding(c *core.Core) *Plugin {
	return &Plugin{core: c}
}

// refresh invalidates pooled runtimes so plugin-contributed skills,
// MCP servers, hooks and tools are picked up by the next assembly.
func (b *Plugin) refresh() error {
	return b.core.ReloadRuntime(b.core.Shell.Context())
}

// List returns every installed plugin.
func (b *Plugin) List() ([]plugins.PluginSummary, error) {
	return b.core.Plugin.Store.List()
}

// SetEnabled toggles one plugin.
func (b *Plugin) SetEnabled(id string, enabled bool) error {
	if err := b.core.Plugin.Store.SetEnabled(id, enabled); err != nil {
		return err
	}
	if !enabled {
		b.core.Plugin.Capability.Stop(id)
	}
	return b.refresh()
}

// Bundle returns the plugin entry bundle source.
func (b *Plugin) Bundle(id string) (string, error) {
	return b.core.Plugin.Store.Bundle(id)
}

// Install copies a plugin directory into the registry.
func (b *Plugin) Install(src string) (plugins.PluginSummary, error) {
	sum, err := b.core.Plugin.Store.Install(src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, b.refresh()
}

// InstallZip installs a plugin from a zip package.
func (b *Plugin) InstallZip(zipPath string) (plugins.PluginSummary, error) {
	sum, err := b.core.Plugin.Store.InstallZip(zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, b.refresh()
}

// Inspect reads a plugin source without installing it.
func (b *Plugin) Inspect(src string) (plugins.PluginSummary, error) {
	return b.core.Plugin.Store.Inspect(src)
}

// Update replaces one plugin from a directory.
func (b *Plugin) Update(
	id, src string,
) (plugins.PluginSummary, error) {
	sum, err := b.core.Plugin.Store.Update(id, src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	b.core.Plugin.Capability.Stop(id)
	return sum, b.refresh()
}

// UpdateZip replaces one plugin from a zip package.
func (b *Plugin) UpdateZip(
	id, zipPath string,
) (plugins.PluginSummary, error) {
	sum, err := b.core.Plugin.Store.UpdateZip(id, zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	b.core.Plugin.Capability.Stop(id)
	return sum, b.refresh()
}

// Rollback restores one plugin's previous version.
func (b *Plugin) Rollback(id string) (plugins.PluginSummary, error) {
	sum, err := b.core.Plugin.Store.Rollback(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	b.core.Plugin.Capability.Stop(id)
	return sum, b.refresh()
}

// PluginToolDTO is the settings view of one plugin tool.
type PluginToolDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Method      string `json:"method"`
}

// Tools returns tools declared by one plugin.
func (b *Plugin) Tools(id string) ([]PluginToolDTO, error) {
	m, err := b.core.Plugin.Store.Manifest(id)
	if err != nil {
		return nil, err
	}
	out := make([]PluginToolDTO, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, PluginToolDTO{
			Name:        t.Name,
			Description: t.Description,
			Method:      t.Method,
		})
	}
	return out, nil
}

// Skills returns the discovered skills contributed by one plugin.
func (b *Plugin) Skills(id string) ([]SkillSummary, error) {
	m, err := b.core.Plugin.Store.Manifest(id)
	if err != nil {
		return nil, err
	}
	dir, _, err := b.core.Plugin.Store.Dir(id)
	if err != nil {
		return nil, err
	}
	roots := pluginSkillRoots(m, dir)
	seen := map[string]bool{}
	var out []SkillSummary
	for _, root := range roots {
		for _, sk := range scanPluginSkillRoot(root) {
			if seen[sk.Path] {
				continue
			}
			seen[sk.Path] = true
			sk.PluginID = id
			sk.PluginName = m.Name
			out = append(out, sk)
		}
	}
	return out, nil
}

// pluginSkillRoots resolves a plugin manifest's declared skill roots
// (defaulting to <plugin>/skills), skipping missing directories.
func pluginSkillRoots(m *plugins.Manifest, dir string) []string {
	if len(m.Skills) == 0 {
		root := filepath.Join(dir, "skills")
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return []string{root}
		}
		return nil
	}
	var roots []string
	for _, rel := range m.Skills {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) ||
			clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			continue
		}
		root := filepath.Join(dir, clean)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	return roots
}

// scanPluginSkillRoot parses every SKILL.md below one plugin skill
// root without depending on an assembled runtime. Symlinks and hidden
// directories are skipped.
func scanPluginSkillRoot(root string) []SkillSummary {
	var out []SkillSummary
	_ = filepath.WalkDir(root, func(
		path string, d os.DirEntry, err error,
	) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil &&
			info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		parsed, parseErr := skills.ParseFile(path)
		if parseErr != nil {
			return nil
		}
		meta := parsed.Metadata
		out = append(out, SkillSummary{
			Name:        meta.Name,
			Description: meta.Description,
			Scope:       "user",
			Path:        path,
		})
		return nil
	})
	return out
}

// pluginOwnerForSkillPath resolves which installed plugin owns one
// discovered skill path, if any.
func pluginOwnerForSkillPath(
	c *core.Core, skillPath string,
) (pluginID, pluginName string, ok bool) {
	if c.Plugin == nil || c.Plugin.Store == nil {
		return "", "", false
	}
	list, err := c.Plugin.Store.List()
	if err != nil {
		return "", "", false
	}
	skillPath = filepath.Clean(skillPath)
	for _, p := range list {
		if p.Error != "" {
			continue
		}
		m, err := c.Plugin.Store.Manifest(p.ID)
		if err != nil {
			continue
		}
		dir, _, err := c.Plugin.Store.Dir(p.ID)
		if err != nil {
			continue
		}
		for _, root := range pluginSkillRoots(m, dir) {
			root = filepath.Clean(root)
			if skillPath == root ||
				strings.HasPrefix(skillPath, root+string(filepath.Separator)) {
				return p.ID, p.Name, true
			}
		}
	}
	return "", "", false
}

// CheckUpdate fetches one plugin's remote update manifest.
func (b *Plugin) CheckUpdate(
	id string,
) (plugins.UpdateInfo, error) {
	ctx := b.core.Shell.Context()
	m, err := b.core.Plugin.Store.Manifest(id)
	if err != nil {
		return plugins.UpdateInfo{}, err
	}
	if m.Update == nil {
		return plugins.UpdateInfo{}, errors.New("plugin has no update source")
	}
	return pluginupdate.CheckWithPolicy(ctx, m.Update.URL, pluginupdate.Policy{})
}

// ApplyUpdate downloads, verifies and applies one plugin update.
func (b *Plugin) ApplyUpdate(
	id string,
) (plugins.PluginSummary, error) {
	ctx := b.core.Shell.Context()
	info, err := b.CheckUpdate(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	zipPath, cleanup, err := pluginupdate.FetchZip(
		ctx, info, pluginupdate.Policy{},
	)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	defer cleanup()
	sum, err := b.core.Plugin.Store.UpdateZip(id, zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	b.core.Plugin.Capability.Stop(id)
	return sum, b.refresh()
}

// Uninstall removes one user plugin and its KV data.
func (b *Plugin) Uninstall(id string) error {
	if b.core.Plugin.Capability != nil {
		_ = b.core.Plugin.Capability.Cleanup(id)
	}
	if err := b.core.Plugin.Store.Uninstall(id); err != nil {
		return err
	}
	b.core.Plugin.KV.RemoveAll(id)
	if b.core.Plugin.Capability != nil {
		b.core.Plugin.Capability.Stop(id)
	}
	return b.refresh()
}

// KVGet returns one plugin KV entry.
func (b *Plugin) KVGet(pluginID, key string) (plugins.KVEntry, error) {
	return b.core.Plugin.KV.Get(pluginID, key)
}

// KVList returns every plugin KV entry.
func (b *Plugin) KVList(pluginID string) ([]plugins.KVEntry, error) {
	return b.core.Plugin.KV.List(pluginID)
}

// KVSet stores one plugin KV entry.
func (b *Plugin) KVSet(pluginID, key, value string) error {
	return b.core.Plugin.KV.Set(pluginID, key, value)
}

// KVDelete removes one plugin KV entry.
func (b *Plugin) KVDelete(pluginID, key string) error {
	return b.core.Plugin.KV.Delete(pluginID, key)
}

// Invoke routes one method call to a capability plugin.
func (b *Plugin) Invoke(
	pluginID, method, args string,
) (string, error) {
	ctx := b.core.Shell.Context()
	if b.core.Plugin.Capability == nil {
		return "", errNotReady("plugin capability")
	}
	var params any
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &params); err != nil {
			return "", errNotReady("invalid args JSON")
		}
	}
	raw, err := b.core.Plugin.Capability.Invoke(ctx, pluginID, method, params)
	return string(raw), err
}
