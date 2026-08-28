package desktop

// Plugin bindings are the minimal host surface for the frontend plugin
// system (see docs/plans/plugin-system.md). Plugins live in
// <dataDir>/plugins/<id>/, declare capabilities in plugin.json, and
// are loaded by the frontend host through PluginBundle. The host is
// fail-closed: unknown permissions or contribution points reject the
// plugin; IDs and bundle paths are validated so a plugin can never
// read outside its own directory.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/GizClaw/opencraft/internal/config"
)

// PluginSummary is the frontend-facing view of one installed plugin.
type PluginSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Entry       string   `json:"entry"`
	Permissions []string `json:"permissions"`
	Enabled     bool     `json:"enabled"`
	Error       string   `json:"error,omitempty"`
	Panels      []string `json:"panels,omitempty"`
	Entries     []string `json:"entries,omitempty"`
}

// pluginManifest mirrors plugins/<id>/plugin.json.
type pluginManifest struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	MinHostVersion string   `json:"minHostVersion,omitempty"`
	Entry          string   `json:"entry"`
	Permissions    []string `json:"permissions"`
	Contributes    struct {
		SettingsPanels []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Order int    `json:"order"`
		} `json:"settingsPanels"`
		SidebarEntries []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Order int    `json:"order"`
		} `json:"sidebarEntries"`
	} `json:"contributes"`
}

// pluginIDRe constrains plugin ids: lowercase start, then lowercase
// letters, digits, dot, underscore or dash.
var pluginIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// allowedPluginPermissions is the closed set of host capabilities a
// plugin may declare. Unknown permissions reject the plugin
// (fail-closed).
var allowedPluginPermissions = map[string]bool{
	"secrets:auth":         true,
	"auth:device":          true,
	"inference:upsert":     true,
	"storage:kv":           true,
	"events:subscribe":     true,
	"commands:register":    true,
	"statusbar:contribute": true,
}

// pluginStateFile records explicit enable/disable choices. A plugin
// without an entry is enabled by default (installed == enabled).
const pluginStateFile = "state.json"

// PluginList returns every installed plugin with its manifest-derived
// metadata and enabled state. A broken plugin is reported with its
// validation error instead of failing the whole list.
func (a *App) PluginList() ([]PluginSummary, error) {
	root, err := a.pluginRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("plugins: create dir: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("plugins: read dir: %w", err)
	}
	state, err := readPluginState(root)
	if err != nil {
		return nil, err
	}
	out := []PluginSummary{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := entry.Name()
		sum := PluginSummary{ID: id}
		if enabled, ok := state[id]; ok {
			sum.Enabled = enabled
		} else {
			sum.Enabled = true
		}
		manifestPath := filepath.Join(root, id, "plugin.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // directory without a manifest is not a plugin
			}
			sum.Error = fmt.Sprintf("plugins: read manifest: %v", err)
			out = append(out, sum)
			continue
		}
		m, verr := parsePluginManifest(id, raw)
		if verr != nil {
			sum.Error = verr.Error()
			out = append(out, sum)
			continue
		}
		sum.Name = m.Name
		sum.Version = m.Version
		sum.Entry = m.Entry
		sum.Permissions = m.Permissions
		for _, p := range m.Contributes.SettingsPanels {
			sum.Panels = append(sum.Panels, p.ID)
		}
		for _, e := range m.Contributes.SidebarEntries {
			sum.Entries = append(sum.Entries, e.ID)
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// PluginBundle returns the plugin entry bundle source, validated so the
// path can never escape the plugin directory.
func (a *App) PluginBundle(id string) (string, error) {
	root, err := a.pluginRoot()
	if err != nil {
		return "", err
	}
	m, err := a.readManifest(root, id)
	if err != nil {
		return "", err
	}
	entry := filepath.Clean(m.Entry)
	if filepath.IsAbs(entry) ||
		entry == ".." ||
		strings.HasPrefix(entry, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plugins: entry escapes plugin dir: %q", m.Entry)
	}
	data, err := os.ReadFile(filepath.Join(root, id, entry))
	if err != nil {
		return "", fmt.Errorf("plugins: read bundle: %w", err)
	}
	return string(data), nil
}

// PluginSetEnabled toggles a plugin's enabled state. The plugin must be
// installed with a valid manifest.
func (a *App) PluginSetEnabled(id string, enabled bool) error {
	root, err := a.pluginRoot()
	if err != nil {
		return err
	}
	if _, err := a.readManifest(root, id); err != nil {
		return err
	}
	state, err := readPluginState(root)
	if err != nil {
		return err
	}
	state[id] = enabled
	return writePluginState(root, state)
}

// PluginInstall copies a plugin folder (containing plugin.json) into
// the plugin root. The installed id comes from the manifest, so the
// source directory name is irrelevant. Reinstalling an existing plugin
// is rejected; uninstall it first.
func (a *App) PluginInstall(src string) (PluginSummary, error) {
	root, err := a.pluginRoot()
	if err != nil {
		return PluginSummary{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: create dir: %w", err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: source: %w", err)
	}
	if !info.IsDir() {
		return PluginSummary{}, fmt.Errorf(
			"plugins: source %q is not a directory", src)
	}
	raw, err := os.ReadFile(filepath.Join(src, "plugin.json"))
	if err != nil {
		return PluginSummary{}, fmt.Errorf(
			"plugins: read source manifest: %w", err)
	}
	// The manifest carries the id; probe it so the directory name does
	// not need to match.
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: decode manifest: %w", err)
	}
	m, err := parsePluginManifest(probe.ID, raw)
	if err != nil {
		return PluginSummary{}, err
	}
	dst := filepath.Join(root, m.ID)
	if _, err := os.Stat(dst); err == nil {
		return PluginSummary{}, fmt.Errorf(
			"plugins: %q is already installed", m.ID)
	} else if !os.IsNotExist(err) {
		return PluginSummary{}, fmt.Errorf(
			"plugins: check destination: %w", err)
	}
	if err := copyPluginDir(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return PluginSummary{}, fmt.Errorf("plugins: install: %w", err)
	}
	sum := PluginSummary{
		ID:          m.ID,
		Name:        m.Name,
		Version:     m.Version,
		Entry:       m.Entry,
		Permissions: m.Permissions,
		Enabled:     true,
	}
	for _, p := range m.Contributes.SettingsPanels {
		sum.Panels = append(sum.Panels, p.ID)
	}
	for _, e := range m.Contributes.SidebarEntries {
		sum.Entries = append(sum.Entries, e.ID)
	}
	return sum, nil
}

// PluginUninstall removes an installed plugin and its enable state.
func (a *App) PluginUninstall(id string) error {
	root, err := a.pluginRoot()
	if err != nil {
		return err
	}
	if _, err := a.readManifest(root, id); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(root, id)); err != nil {
		return fmt.Errorf("plugins: remove %q: %w", id, err)
	}
	a.removePluginKVData(id)
	state, err := readPluginState(root)
	if err != nil {
		return err
	}
	delete(state, id)
	return writePluginState(root, state)
}

func (a *App) pluginRoot() (string, error) {
	if a.pluginDir != "" {
		return a.pluginDir, nil
	}
	dataDir, err := config.UserDataDir()
	if err != nil {
		return "", fmt.Errorf("plugins: user data dir: %w", err)
	}
	return filepath.Join(dataDir, "plugins"), nil
}

func (a *App) readManifest(root, id string) (*pluginManifest, error) {
	if !pluginIDRe.MatchString(id) {
		return nil, fmt.Errorf("plugins: invalid plugin id %q", id)
	}
	raw, err := os.ReadFile(filepath.Join(root, id, "plugin.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("plugins: plugin %q is not installed", id)
		}
		return nil, fmt.Errorf("plugins: read manifest: %w", err)
	}
	return parsePluginManifest(id, raw)
}

func parsePluginManifest(id string, raw []byte) (*pluginManifest, error) {
	var m pluginManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("plugins: decode manifest: %w", err)
	}
	if m.ID != id {
		return nil, fmt.Errorf(
			"plugins: manifest id %q does not match directory %q", m.ID, id)
	}
	if !pluginIDRe.MatchString(m.ID) {
		return nil, fmt.Errorf("plugins: invalid plugin id %q", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return nil, fmt.Errorf("plugins: manifest requires name")
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, fmt.Errorf("plugins: manifest requires version")
	}
	if strings.TrimSpace(m.Entry) == "" {
		return nil, fmt.Errorf("plugins: manifest requires entry")
	}
	for _, p := range m.Permissions {
		if !allowedPluginPermissions[p] {
			return nil, fmt.Errorf(
				"plugins: unknown permission %q", p)
		}
	}
	seenPanels := map[string]bool{}
	for _, p := range m.Contributes.SettingsPanels {
		if p.ID == "" || seenPanels[p.ID] {
			return nil, fmt.Errorf(
				"plugins: duplicate or empty settings panel id %q", p.ID)
		}
		seenPanels[p.ID] = true
	}
	seenEntries := map[string]bool{}
	for _, e := range m.Contributes.SidebarEntries {
		if e.ID == "" || seenEntries[e.ID] {
			return nil, fmt.Errorf(
				"plugins: duplicate or empty sidebar entry id %q", e.ID)
		}
		seenEntries[e.ID] = true
	}
	return &m, nil
}

// copyPluginDir copies one plugin source tree into the plugin root,
// skipping dotfiles (".DS_Store", ".git", ...). Files are 0600 and
// directories 0700 like the rest of the app state.
func copyPluginDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		if strings.HasPrefix(filepath.Base(rel), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

func readPluginState(root string) (map[string]bool, error) {
	state := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, pluginStateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("plugins: read state: %w", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("plugins: decode state: %w", err)
	}
	return state, nil
}

func writePluginState(root string, state map[string]bool) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, pluginStateFile)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("plugins: write state: %w", err)
	}
	return nil
}
