// Package plugins is the pure core of the OpenCraft plugin system: the
// on-disk plugin registry, manifest validation, per-plugin KV storage
// and the device-authorization auth primitives. It has no dependency
// on the desktop shell; wails bindings in internal/desktop delegate to
// these types.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/GizClaw/opencraft/internal/plugins/runtime"
)

// idRe constrains plugin/provider ids: lowercase start, then lowercase
// letters, digits, dot, underscore or dash.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidateID reports whether id is a valid plugin/provider id.
func ValidateID(id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("plugins: invalid id %q", id)
	}
	return nil
}

// AllowedPermissions is the closed set of host capabilities a plugin
// may declare. Unknown permissions reject the plugin (fail-closed).
var AllowedPermissions = map[string]bool{
	"secrets:auth":         true,
	"inference:upsert":     true,
	"storage:kv":           true,
	"events:subscribe":     true,
	"commands:register":    true,
	"statusbar:contribute": true,
}

// CheckPermissions validates a manifest permission list.
func CheckPermissions(perms []string) error {
	for _, p := range perms {
		if !AllowedPermissions[p] {
			return fmt.Errorf("plugins: unknown permission %q", p)
		}
	}
	return nil
}

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

// manifest mirrors plugins/<id>/plugin.json.
type manifest struct {
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
	// Capability declares an optional subprocess runtime for the
	// plugin (see internal/plugins/runtime).
	Capability *runtime.Capability `json:"capability,omitempty"`
}

// pluginStateFile records explicit enable/disable choices. A plugin
// without an entry is enabled by default (installed == enabled).
const pluginStateFile = "state.json"

// Store is the on-disk plugin registry rooted at a directory (usually
// <dataDir>/plugins).
type Store struct {
	root string
}

// NewStore returns a registry rooted at root.
func NewStore(root string) *Store {
	return &Store{root: root}
}

// List returns every installed plugin with its manifest metadata and
// enabled state. A broken plugin is reported with its validation error
// instead of failing the whole list.
func (s *Store) List() ([]PluginSummary, error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("plugins: create dir: %w", err)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("plugins: read dir: %w", err)
	}
	state, err := s.readState()
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
		raw, err := os.ReadFile(filepath.Join(s.root, id, "plugin.json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue // directory without a manifest is not a plugin
			}
			sum.Error = fmt.Sprintf("plugins: read manifest: %v", err)
			out = append(out, sum)
			continue
		}
		m, verr := parseManifest(id, raw)
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

// Bundle returns the plugin entry bundle source, validated so the path
// can never escape the plugin directory.
func (s *Store) Bundle(id string) (string, error) {
	m, err := s.readManifest(id)
	if err != nil {
		return "", err
	}
	entry := filepath.Clean(m.Entry)
	if filepath.IsAbs(entry) ||
		entry == ".." ||
		strings.HasPrefix(entry, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plugins: entry escapes plugin dir: %q", m.Entry)
	}
	data, err := os.ReadFile(filepath.Join(s.root, id, entry))
	if err != nil {
		return "", fmt.Errorf("plugins: read bundle: %w", err)
	}
	return string(data), nil
}

// Capability returns the declared subprocess runtime for an installed
// plugin, if any.
func (s *Store) Capability(id string) (runtime.Capability, bool, error) {
	m, err := s.readManifest(id)
	if err != nil {
		return runtime.Capability{}, false, err
	}
	if m.Capability == nil {
		return runtime.Capability{}, false, nil
	}
	return *m.Capability, true, nil
}

// SetEnabled toggles a plugin's enabled state. The plugin must be
// installed with a valid manifest.
func (s *Store) SetEnabled(id string, enabled bool) error {
	if _, err := s.readManifest(id); err != nil {
		return err
	}
	state, err := s.readState()
	if err != nil {
		return err
	}
	state[id] = enabled
	return s.writeState(state)
}

// Install copies a plugin folder (containing plugin.json) into the
// registry. The installed id comes from the manifest, so the source
// directory name is irrelevant. Reinstalling an existing plugin is
// rejected; uninstall it first.
func (s *Store) Install(src string) (PluginSummary, error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
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
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: decode manifest: %w", err)
	}
	m, err := parseManifest(probe.ID, raw)
	if err != nil {
		return PluginSummary{}, err
	}
	dst := filepath.Join(s.root, m.ID)
	if _, err := os.Stat(dst); err == nil {
		return PluginSummary{}, fmt.Errorf(
			"plugins: %q is already installed", m.ID)
	} else if !os.IsNotExist(err) {
		return PluginSummary{}, fmt.Errorf(
			"plugins: check destination: %w", err)
	}
	if err := copyDir(src, dst); err != nil {
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

// Uninstall removes an installed plugin and its enable state. Plugin
// data (KV) is removed by the caller via KVStore.RemoveAll.
func (s *Store) Uninstall(id string) error {
	if _, err := s.readManifest(id); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.root, id)); err != nil {
		return fmt.Errorf("plugins: remove %q: %w", id, err)
	}
	state, err := s.readState()
	if err != nil {
		return err
	}
	delete(state, id)
	return s.writeState(state)
}

func (s *Store) readManifest(id string) (*manifest, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(s.root, id, "plugin.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("plugins: plugin %q is not installed", id)
		}
		return nil, fmt.Errorf("plugins: read manifest: %w", err)
	}
	return parseManifest(id, raw)
}

func parseManifest(id string, raw []byte) (*manifest, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("plugins: decode manifest: %w", err)
	}
	if m.ID != id {
		return nil, fmt.Errorf(
			"plugins: manifest id %q does not match directory %q", m.ID, id)
	}
	if err := ValidateID(m.ID); err != nil {
		return nil, err
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
	if err := CheckPermissions(m.Permissions); err != nil {
		return nil, err
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
	if err := validateCapability(m.Capability); err != nil {
		return nil, fmt.Errorf("plugins: %w", err)
	}
	return &m, nil
}

// validateCapability checks a declared subprocess runtime: the binary
// must be a relative path inside the plugin directory and the protocol
// version must be positive.
func validateCapability(cap *runtime.Capability) error {
	if cap == nil {
		return nil
	}
	if cap.Binary == "" {
		return fmt.Errorf("capability.binary is required")
	}
	bin := filepath.Clean(cap.Binary)
	if filepath.IsAbs(bin) ||
		bin == ".." ||
		strings.HasPrefix(bin, ".."+string(filepath.Separator)) {
		return fmt.Errorf("capability.binary escapes plugin dir: %q", cap.Binary)
	}
	if cap.Protocol <= 0 {
		return fmt.Errorf("capability.protocol must be positive")
	}
	return nil
}

// copyDir copies one plugin source tree, skipping dotfiles (".DS_Store",
// ".git", ...). Files are 0600 and directories 0700.
func copyDir(src, dst string) error {
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

func (s *Store) readState() (map[string]bool, error) {
	state := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(s.root, pluginStateFile))
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

func (s *Store) writeState(state map[string]bool) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.root, pluginStateFile)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("plugins: write state: %w", err)
	}
	return nil
}
