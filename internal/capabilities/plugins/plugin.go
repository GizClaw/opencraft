// Package plugins is the pure core of the OpenCraft plugin system: the
// on-disk plugin registry, manifest validation, per-plugin KV storage
// and the device-authorization auth primitives. It has no dependency
// on the desktop shell; wails bindings in internal/desktop delegate to
// these types.
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
)

// idRe constrains plugin/provider ids: lowercase start, then lowercase
// letters, digits, dot, underscore or dash.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// toolNameRe constrains agent-facing tool names: lowercase start,
// then lowercase letters, digits, underscore or dash. Providers
// (OpenAI-compatible /responses) require tool names to match
// ^[a-zA-Z0-9_-]+$, so dot is deliberately not allowed here.
var toolNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Bounds on agent-facing manifest declarations. These keep plugin.json
// and the tool definitions derived from it bounded before they reach
// the model context or the tool registry.
const (
	maxPluginManifestBytes    = 1 << 20 // 1 MiB
	maxPluginSkillCount       = 32
	maxPluginHookCount        = 16
	maxPluginMCPServerCount   = 16
	maxPluginToolCount        = 64
	maxPluginPathLen          = 256
	maxPluginDescriptionChars = 1024
	maxPluginInputSchemaBytes = 32 << 10 // 32 KiB
	maxPluginMethodLen        = 128
	maxPluginCommandLen       = 1024
	maxPluginURLLen           = 2048
	maxPluginEnvCount         = 32
	maxPluginEnvKeyLen        = 128
	maxPluginEnvValueLen      = 4096
)

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
	"storage:kv":           true,
	"events:subscribe":     true,
	"commands:register":    true,
	"statusbar:contribute": true,
	"tools:expose":         true,
	"sessions:import":      true,
	"skills:contribute":    true,
	"mcp:contribute":       true,
	"hooks:register":       true,
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
	// Builtin marks an app-bundled read-only plugin (see Store).
	Builtin bool     `json:"builtin,omitempty"`
	Error   string   `json:"error,omitempty"`
	Panels  []string `json:"panels,omitempty"`
	Entries []string `json:"entries,omitempty"`
	// ShadowsBuiltin marks a user plugin that overrides an app-bundled
	// builtin with the same id. BuiltinVersion reports the version of
	// the shadowed builtin so the UI can compare it with the user
	// version.
	ShadowsBuiltin bool   `json:"shadowsBuiltin,omitempty"`
	BuiltinVersion string `json:"builtinVersion,omitempty"`
	// Agent-facing capability flags (skills / MCP / hooks / tools).
	HasSkills bool `json:"hasSkills,omitempty"`
	HasMCP    bool `json:"hasMcp,omitempty"`
	HasHooks  bool `json:"hasHooks,omitempty"`
	HasTools  bool `json:"hasTools,omitempty"`
	// HasUpdate reports whether the plugin declares an update.url.
	HasUpdate bool `json:"hasUpdate,omitempty"`
	// CanRollback reports whether a rollback snapshot of the previous
	// version is available (user plugins only).
	CanRollback bool `json:"canRollback,omitempty"`
}

// PluginTool is one agent-callable tool exposed by a capability
// subprocess. The host only routes by method name; the plugin owns the
// semantics of its tool methods.
type PluginTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Method      string          `json:"method"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	// MutatesState defaults to true when omitted (conservative).
	MutatesState *bool `json:"mutatesState,omitempty"`
}

// PluginMCPServer is one MCP server contributed by a plugin. Stdio
// commands may be relative to the plugin directory; the agent host
// resolves them before handing them to the MCP source.
type PluginMCPServer struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // stdio | http
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
}

// PluginUpdateSource declares where opencraft can check for a newer
// version of the plugin. The URL must return the update manifest shape
// described by internal/plugins/update.
type PluginUpdateSource struct {
	URL string `json:"url"`
}

// UpdateInfo is the update manifest returned by a plugin's update.url
// endpoint. Checksum must be "sha256:<hex>" and is verified before an
// update package is applied.
type UpdateInfo struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	Checksum    string `json:"checksum"`
	Changelog   string `json:"changelog,omitempty"`
}

// Manifest mirrors plugins/<id>/plugin.json.
type Manifest struct {
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
	// Agent-facing capabilities. Each group requires its matching
	// permission (skills:contribute, mcp:contribute, hooks:register,
	// tools:expose) and is ignored otherwise.
	Skills     []string          `json:"skills,omitempty"`
	McpServers []PluginMCPServer `json:"mcpServers,omitempty"`
	Hooks      []string          `json:"hooks,omitempty"`
	Tools      []PluginTool      `json:"tools,omitempty"`
	// Update points at the plugin's update manifest endpoint.
	Update *PluginUpdateSource `json:"update,omitempty"`
}

// pluginStateFile records explicit enable/disable choices. A plugin
// without an entry is enabled by default (installed == enabled).
const pluginStateFile = "state.json"

// Store is the plugin registry: a writable user root (usually
// <dataDir>/plugins) plus an optional read-only builtin root
// (app-bundled plugins, see runtime.BuiltinPluginRoot). User plugins
// shadow builtins with the same id; builtins are always present, can be
// disabled but never uninstalled.
type Store struct {
	root        string
	builtin     string
	hostVersion string
	mu          sync.Mutex
}

// NewStore returns a registry rooted at root.
func NewStore(root string) *Store {
	return &Store{root: root, builtin: runtime.BuiltinPluginRoot()}
}

// SetHostVersion records the running host version for minHostVersion
// enforcement. An empty value disables the check (tests/CLI).
func (s *Store) SetHostVersion(v string) { s.hostVersion = v }

// List returns every installed plugin with its manifest metadata and
// enabled state. A broken plugin is reported with its validation error
// instead of failing the whole list.
func (s *Store) List() ([]PluginSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("plugins: create dir: %w", err)
	}
	state, err := s.readState()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := s.scanDir(s.root, state, false, seen)
	if s.builtin != "" {
		out = append(out, s.scanDir(s.builtin, state, true, seen)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// scanDir lists the plugins of one root directory. user state is shared
// (builtin enable/disable choices persist in the user root's state
// file). ids already in seen are skipped so the user root wins over the
// builtin root; every listed id is recorded in seen.
func (s *Store) scanDir(
	root string,
	state map[string]bool,
	builtin bool,
	seen map[string]bool,
) []PluginSummary {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []PluginSummary{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := entry.Name()
		if seen[id] {
			continue
		}
		sum := PluginSummary{ID: id, Builtin: builtin}
		if enabled, ok := state[id]; ok {
			sum.Enabled = enabled
		} else {
			sum.Enabled = true
		}
		raw, err := os.ReadFile(filepath.Join(root, id, "plugin.json"))
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
		sum.HasSkills = len(m.Skills) > 0
		if !sum.HasSkills && manifestHasPermission(m, "skills:contribute") &&
			dirExists(filepath.Join(root, id, "skills")) {
			sum.HasSkills = true
		}
		sum.HasMCP = len(m.McpServers) > 0
		sum.HasHooks = len(m.Hooks) > 0
		sum.HasTools = len(m.Tools) > 0
		sum.HasUpdate = m.Update != nil
		if !builtin {
			sum.CanRollback = rollbackAvailable(s.root, id)
			if v := s.builtinVersion(id); v != "" {
				sum.ShadowsBuiltin = true
				sum.BuiltinVersion = v
			}
		}
		for _, p := range m.Contributes.SettingsPanels {
			sum.Panels = append(sum.Panels, p.ID)
		}
		for _, e := range m.Contributes.SidebarEntries {
			sum.Entries = append(sum.Entries, e.ID)
		}
		out = append(out, sum)
		seen[id] = true
	}
	return out
}

// Bundle returns the plugin entry bundle source, validated so the path
// can never escape the plugin directory.
func (s *Store) Bundle(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, _, err := s.pluginDir(id)
	if err != nil {
		return "", err
	}
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
	data, err := os.ReadFile(filepath.Join(dir, entry))
	if err != nil {
		return "", fmt.Errorf("plugins: read bundle: %w", err)
	}
	return string(data), nil
}

// Capability returns the declared subprocess runtime for an installed
// plugin, if any.
func (s *Store) Capability(id string) (runtime.Capability, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if err := s.checkHostVersion(m); err != nil {
		return PluginSummary{}, err
	}
	if bv := s.builtinVersion(m.ID); bv != "" {
		cmp, err := compareVersions(m.Version, bv)
		if err != nil {
			return PluginSummary{}, fmt.Errorf("plugins: compare with builtin %q: %w", bv, err)
		}
		if cmp < 0 {
			return PluginSummary{}, fmt.Errorf(
				"plugins: %q version %q is older than the builtin version %q; install %q or newer to override it",
				m.ID, m.Version, bv, bv)
		}
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
		telemetry.WarnErr(context.Background(),
			"plugins: remove partial install failed", os.RemoveAll(dst))
		return PluginSummary{}, fmt.Errorf("plugins: install: %w", err)
	}
	if err := preparePluginDir(dst, m); err != nil {
		telemetry.WarnErr(context.Background(),
			"plugins: remove invalid install failed", os.RemoveAll(dst))
		return PluginSummary{}, err
	}
	return s.withBuiltinInfo(summaryFromManifest(m, dst, false)), nil
}

// Inspect reads a plugin source (a folder containing plugin.json or a
// zip package) and returns its manifest summary without installing
// it. The install dialog uses it to warn when the source would
// override a builtin with the same id.
func (s *Store) Inspect(src string) (PluginSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, cleanup, err := sourcePluginDir(src)
	if err != nil {
		return PluginSummary{}, err
	}
	defer cleanup()
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: read source manifest: %w", err)
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
	if err := s.checkHostVersion(m); err != nil {
		return PluginSummary{}, err
	}
	sum := summaryFromManifest(m, dir, false)
	return s.withBuiltinInfo(sum), nil
}

// sourcePluginDir resolves a plugin source: a directory containing
// plugin.json, or a zip package (extracted to a temporary directory).
func sourcePluginDir(src string) (string, func(), error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", nil, fmt.Errorf("plugins: source: %w", err)
	}
	if info.IsDir() {
		return src, func() {}, nil
	}
	return extractPluginZip(src)
}

// withBuiltinInfo annotates a user-plugin summary when a builtin with
// the same id exists, so installers/updaters can surface the override
// relationship immediately.
func (s *Store) withBuiltinInfo(sum PluginSummary) PluginSummary {
	if sum.Builtin || s.builtin == "" {
		return sum
	}
	if v := s.builtinVersion(sum.ID); v != "" {
		sum.ShadowsBuiltin = true
		sum.BuiltinVersion = v
	}
	return sum
}

// builtinVersion reads the manifest version of the app-bundled builtin
// with id, or "" when no builtin exists or its manifest is unreadable.
func (s *Store) builtinVersion(id string) string {
	if s.builtin == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(s.builtin, id, "plugin.json"))
	if err != nil {
		return ""
	}
	var probe struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Version == "" {
		return ""
	}
	return probe.Version
}

// summaryFromManifest builds the frontend-facing summary for one
// installed manifest. canRollback reports whether a rollback snapshot
// currently exists.
func summaryFromManifest(m *Manifest, dir string, canRollback bool) PluginSummary {
	sum := PluginSummary{
		ID:          m.ID,
		Name:        m.Name,
		Version:     m.Version,
		Entry:       m.Entry,
		Permissions: m.Permissions,
		Enabled:     true,
		HasSkills:   len(m.Skills) > 0,
		HasMCP:      len(m.McpServers) > 0,
		HasHooks:    len(m.Hooks) > 0,
		HasTools:    len(m.Tools) > 0,
		HasUpdate:   m.Update != nil,
		CanRollback: canRollback,
	}
	if !sum.HasSkills && manifestHasPermission(m, "skills:contribute") &&
		dirExists(filepath.Join(dir, "skills")) {
		sum.HasSkills = true
	}
	for _, p := range m.Contributes.SettingsPanels {
		sum.Panels = append(sum.Panels, p.ID)
	}
	for _, e := range m.Contributes.SidebarEntries {
		sum.Entries = append(sum.Entries, e.ID)
	}
	return sum
}

// Update replaces an installed user plugin with a newer source
// directory. The previous version is snapshotted under
// <root>/.backups/<id> for Rollback. The plugin's enabled state, KV
// data, secrets and inference profile are preserved.
func (s *Store) Update(id, src string) (PluginSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ValidateID(id); err != nil {
		return PluginSummary{}, err
	}
	dir, builtin, err := s.pluginDir(id)
	if err != nil {
		return PluginSummary{}, err
	}
	if builtin {
		return PluginSummary{}, fmt.Errorf(
			"plugins: %q is a builtin plugin and cannot be updated", id)
	}
	info, err := os.Stat(src)
	if err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: update source: %w", err)
	}
	if !info.IsDir() {
		return PluginSummary{}, fmt.Errorf(
			"plugins: update source %q is not a directory", src)
	}
	cur, err := s.readManifest(id)
	if err != nil {
		return PluginSummary{}, err
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
	if m.ID != id {
		return PluginSummary{}, fmt.Errorf(
			"plugins: update source manifest id %q does not match %q", m.ID, id)
	}
	cmp, err := compareVersions(m.Version, cur.Version)
	if err != nil {
		return PluginSummary{}, err
	}
	if cmp <= 0 {
		return PluginSummary{}, fmt.Errorf(
			"plugins: update %q version %q is not newer than installed %q",
			id, m.Version, cur.Version)
	}
	if bv := s.builtinVersion(id); bv != "" {
		cmp, err := compareVersions(m.Version, bv)
		if err != nil {
			return PluginSummary{}, fmt.Errorf("plugins: compare with builtin %q: %w", bv, err)
		}
		if cmp < 0 {
			return PluginSummary{}, fmt.Errorf(
				"plugins: update %q version %q is older than the builtin version %q; update to %q or newer",
				id, m.Version, bv, bv)
		}
	}
	if err := s.checkHostVersion(m); err != nil {
		return PluginSummary{}, err
	}

	tmp, err := os.MkdirTemp(s.root, ".plugin-update-")
	if err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: update temp dir: %w", err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"plugins: remove update temp failed", os.RemoveAll(tmp))
	}()
	if err := copyDir(src, tmp); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: stage update: %w", err)
	}
	if err := preparePluginDir(tmp, m); err != nil {
		return PluginSummary{}, err
	}

	backup := filepath.Join(s.root, ".backups", id)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: create backup dir: %w", err)
	}
	// Stage the current version as the pending snapshot first; the
	// previous rollback snapshot is only replaced after the new version
	// is live, so a failed swap never loses the last good rollback.
	pending := backup + ".pending"
	telemetry.WarnErr(context.Background(),
		"plugins: clear pending rollback snapshot failed",
		os.RemoveAll(pending))
	if err := os.Rename(dir, pending); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: backup %q: %w", id, err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		restoreErr := os.Rename(pending, dir)
		telemetry.WarnErr(context.Background(),
			"plugins: restore current plugin after update swap failure", restoreErr)
		return PluginSummary{}, fmt.Errorf(
			"plugins: replace %q: %w (restore: %v)", id, err, restoreErr)
	}
	old := backup + ".old"
	telemetry.WarnErr(context.Background(),
		"plugins: clear previous rollback snapshot failed",
		os.RemoveAll(old))
	if _, statErr := os.Stat(backup); statErr == nil {
		if err := os.Rename(backup, old); err != nil {
			return PluginSummary{}, fmt.Errorf(
				"plugins: move previous rollback snapshot %q: %w", id, err)
		}
	} else if !os.IsNotExist(statErr) {
		return PluginSummary{}, fmt.Errorf(
			"plugins: stat rollback snapshot %q: %w", id, statErr)
	}
	if err := os.Rename(pending, backup); err != nil {
		restoreErr := os.Rename(old, backup)
		telemetry.WarnErr(context.Background(),
			"plugins: restore previous rollback snapshot failed", restoreErr)
		return PluginSummary{}, fmt.Errorf(
			"plugins: commit rollback snapshot %q: %w (restore previous snapshot: %v)",
			id, err, restoreErr)
	}
	telemetry.WarnErr(context.Background(),
		"plugins: remove old rollback snapshot failed", os.RemoveAll(old))

	sum := summaryFromManifest(m, dir, true)
	if state, err := s.readState(); err == nil {
		if enabled, ok := state[id]; ok {
			sum.Enabled = enabled
		}
	}
	return s.withBuiltinInfo(sum), nil
}

// UpdateZip updates a plugin from a zip package. The archive layout
// follows InstallZip.
func (s *Store) UpdateZip(id, zipPath string) (PluginSummary, error) {
	dir, cleanup, err := extractPluginZip(zipPath)
	if err != nil {
		return PluginSummary{}, err
	}
	defer cleanup()
	return s.Update(id, dir)
}

// Rollback restores the previous version snapshot created by Update.
// The current (new) version is discarded; enabled state, KV data,
// secrets and inference profile are preserved.
func (s *Store) Rollback(id string) (PluginSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ValidateID(id); err != nil {
		return PluginSummary{}, err
	}
	backup := filepath.Join(s.root, ".backups", id)
	raw, err := os.ReadFile(filepath.Join(backup, "plugin.json"))
	if err != nil {
		return PluginSummary{}, fmt.Errorf(
			"plugins: %q has no rollback snapshot", id)
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return PluginSummary{}, fmt.Errorf("plugins: decode rollback manifest: %w", err)
	}
	m, err := parseManifest(probe.ID, raw)
	if err != nil {
		return PluginSummary{}, err
	}
	if m.ID != id {
		return PluginSummary{}, fmt.Errorf(
			"plugins: rollback manifest id %q does not match %q", m.ID, id)
	}
	if err := s.checkHostVersion(m); err != nil {
		return PluginSummary{}, err
	}
	if err := preparePluginDir(backup, m); err != nil {
		return PluginSummary{}, err
	}
	dir := filepath.Join(s.root, id)
	pending := filepath.Join(s.root, ".backups", id+".discard")
	telemetry.WarnErr(context.Background(),
		"plugins: clear discard snapshot failed", os.RemoveAll(pending))
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, pending); err != nil {
			return PluginSummary{}, fmt.Errorf(
				"plugins: move current %q aside: %w", id, err)
		}
	} else if !os.IsNotExist(err) {
		return PluginSummary{}, fmt.Errorf("plugins: stat %q: %w", id, err)
	}
	if err := os.Rename(backup, dir); err != nil {
		restoreErr := os.Rename(pending, dir)
		telemetry.WarnErr(context.Background(),
			"plugins: restore current plugin after rollback failure", restoreErr)
		return PluginSummary{}, fmt.Errorf(
			"plugins: restore %q: %w (restore current: %v)", id, err, restoreErr)
	}
	telemetry.WarnErr(context.Background(),
		"plugins: remove discard snapshot failed", os.RemoveAll(pending))
	sum := summaryFromManifest(m, dir, false)
	if state, err := s.readState(); err == nil {
		if enabled, ok := state[id]; ok {
			sum.Enabled = enabled
		}
	}
	return s.withBuiltinInfo(sum), nil
}

// checkHostVersion enforces manifest.minHostVersion against the
// recorded host version when both are present.
func (s *Store) checkHostVersion(m *Manifest) error {
	if m.MinHostVersion == "" || s.hostVersion == "" {
		return nil
	}
	cmp, err := compareVersions(m.MinHostVersion, s.hostVersion)
	if err != nil {
		return fmt.Errorf("plugins: %w", err)
	}
	if cmp > 0 {
		return fmt.Errorf(
			"plugins: %q requires host %s, running %s",
			m.ID, m.MinHostVersion, s.hostVersion)
	}
	return nil
}

const (
	maxVersionLen        = 64
	maxVersionSegments   = 4
	maxPrereleaseIDs     = 8
	maxVersionIdentifier = 32
)

// parsedVersion is a semver-shaped dotted numeric version with an
// optional prerelease. Build metadata is ignored for ordering.
type parsedVersion struct {
	core []int
	pre  []string
}

// parseVersion accepts "1", "1.2", "1.2.3", optional "v" prefix, an
// optional "-prerelease" suffix (dot-separated identifiers) and
// "+build" metadata. Numeric segments must be non-negative integers
// without leading zeros; prerelease identifiers are bounded.
func parseVersion(v string) (parsedVersion, error) {
	orig := v
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" || len(v) > maxVersionLen {
		return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	if v == "" {
		return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
	}
	parts := strings.Split(v, ".")
	if len(parts) > maxVersionSegments {
		return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
	}
	core := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
		}
		core = append(core, n)
	}
	var preIDs []string
	if pre != "" {
		ids := strings.Split(pre, ".")
		if len(ids) > maxPrereleaseIDs {
			return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
		}
		for _, id := range ids {
			if id == "" || len(id) > maxVersionIdentifier {
				return parsedVersion{}, fmt.Errorf("invalid version %q", orig)
			}
			preIDs = append(preIDs, id)
		}
	}
	return parsedVersion{core: core, pre: preIDs}, nil
}

// validateVersion checks that v is a supported version string.
func validateVersion(v string) error {
	_, err := parseVersion(v)
	return err
}

// ValidateVersion is the exported validation entrypoint used by the
// update checker before accepting a remote version.
func ValidateVersion(v string) error { return validateVersion(v) }

// compareVersions orders two version strings semver-style: dotted
// numeric core, then prerelease precedence (release > any prerelease,
// numeric identifiers sort before alphanumeric, shorter prerelease
// sorts first when all identifiers are equal). Missing core segments
// count as zero so "1" and "1.0.0" compare equal.
func compareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("plugins: %w", err)
	}
	pb, err := parseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("plugins: %w", err)
	}
	max := len(pa.core)
	if len(pb.core) > max {
		max = len(pb.core)
	}
	for i := 0; i < max; i++ {
		var x, y int
		if i < len(pa.core) {
			x = pa.core[i]
		}
		if i < len(pb.core) {
			y = pb.core[i]
		}
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	if len(pa.pre) == 0 && len(pb.pre) == 0 {
		return 0, nil
	}
	if len(pa.pre) == 0 {
		return 1, nil
	}
	if len(pb.pre) == 0 {
		return -1, nil
	}
	for i := 0; i < len(pa.pre) || i < len(pb.pre); i++ {
		if i >= len(pa.pre) {
			return -1, nil
		}
		if i >= len(pb.pre) {
			return 1, nil
		}
		x := pa.pre[i]
		y := pb.pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn < yn {
				return -1, nil
			}
			if xn > yn {
				return 1, nil
			}
		case xerr == nil:
			return -1, nil // numeric identifiers sort below alphanumeric
		case yerr == nil:
			return 1, nil
		default:
			if x < y {
				return -1, nil
			}
			if x > y {
				return 1, nil
			}
		}
	}
	return 0, nil
}

// rollbackAvailable reports whether a rollback snapshot exists for id.
func rollbackAvailable(root, id string) bool {
	path := filepath.Join(root, ".backups", id, "plugin.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.ID != id {
		return false
	}
	_, err = parseManifest(probe.ID, raw)
	return err == nil
}

// validateInstalledAgentResources checks that every declared agent
// capability actually exists after install: skills directories, hooks
// files (valid JSON), and plugin-relative MCP stdio commands. Bare
// PATH commands like "npx" are left for the runtime to resolve.
func validateInstalledAgentResources(dst string, m *Manifest) error {
	for i, rel := range m.Skills {
		dir := filepath.Join(dst, rel)
		if !dirExists(dir) {
			return fmt.Errorf("plugins: skills[%d] %q is missing or not a directory", i, rel)
		}
	}
	for i, rel := range m.Hooks {
		path := filepath.Join(dst, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("plugins: hooks[%d] %q is missing: %w", i, rel, err)
		}
		if !json.Valid(data) {
			return fmt.Errorf("plugins: hooks[%d] %q is not valid JSON", i, rel)
		}
	}
	for i, srv := range m.McpServers {
		if srv.Transport != "stdio" {
			continue
		}
		cmd := srv.Command
		if filepath.IsAbs(cmd) ||
			strings.Contains(cmd, "/") ||
			strings.Contains(cmd, `\`) ||
			strings.HasPrefix(cmd, ".") {
			path := cmd
			if !filepath.IsAbs(path) {
				path = filepath.Join(dst, cmd)
			}
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				return fmt.Errorf(
					"plugins: mcpServers[%d] (%s): stdio command %q is missing",
					i, srv.Name, srv.Command)
			}
		}
	}
	return nil
}

// preparePluginDir validates every declared agent resource and makes
// the capability binary executable (ad-hoc signed on macOS). It is
// the shared gate for install, update staging and rollback restore.
func preparePluginDir(dir string, m *Manifest) error {
	if err := validateInstalledAgentResources(dir, m); err != nil {
		return err
	}
	if m.Capability == nil {
		return nil
	}
	bin := filepath.Join(dir, m.Capability.Binary)
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("plugins: make capability binary executable: %w", err)
	}
	if err := signAdHoc(bin); err != nil {
		return err
	}
	return nil
}

// signAdHoc ad-hoc codesigns a capability binary on macOS. An unsigned
// Mach-O binary under ~/.opencraft is killed by the system's security
// machinery (SIGKILL on exec); ad-hoc signing marks it as locally
// trusted. No-op on other platforms.
func signAdHoc(path string) error {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("codesign"); err != nil {
		return fmt.Errorf("plugins: codesign unavailable: %w", err)
	}
	// Already signed (e.g. from a packaged release): leave it.
	if _, err := exec.Command("codesign", "--verify", "-q", path).CombinedOutput(); err == nil {
		return nil
	}
	if out, err := exec.Command("codesign", "-s", "-", path).CombinedOutput(); err != nil {
		// "is already signed" is not a real failure for a re-sign.
		if strings.Contains(string(out), "already signed") {
			return nil
		}
		return fmt.Errorf("plugins: ad-hoc sign %q: %w: %s", path, err, out)
	}
	return nil
}

// Uninstall removes an installed plugin and its enable state. Plugin
// data (KV) is removed by the caller via KVStore.RemoveAll.
func (s *Store) Uninstall(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, builtin, err := s.pluginDir(id)
	if err != nil {
		return err
	}
	if builtin {
		return fmt.Errorf(
			"plugins: %q is a builtin plugin and cannot be uninstalled; disable it instead",
			id)
	}
	if err := os.RemoveAll(filepath.Join(s.root, id)); err != nil {
		return fmt.Errorf("plugins: remove %q: %w", id, err)
	}
	telemetry.WarnErr(context.Background(),
		"plugins: remove plugin backups failed",
		os.RemoveAll(filepath.Join(s.root, ".backups", id)))
	state, err := s.readState()
	if err != nil {
		return err
	}
	delete(state, id)
	return s.writeState(state)
}

func (s *Store) readManifest(id string) (*Manifest, error) {
	dir, _, err := s.pluginDir(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, fmt.Errorf("plugins: read manifest: %w", err)
	}
	return parseManifest(id, raw)
}

// Manifest returns the parsed manifest of an installed plugin. It is
// the read path for agent-facing capability discovery (skills, MCP
// servers, hooks, tools).
func (s *Store) Manifest(id string) (*Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readManifest(id)
}

// Dir resolves the directory holding an installed plugin (user root
// wins over the builtin root). builtin reports whether the plugin is
// app-bundled and therefore read-only.
func (s *Store) Dir(id string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pluginDir(id)
}

// pluginDir resolves the directory holding an installed plugin: the
// user root wins, otherwise the builtin root. builtin reports whether
// the plugin lives in the read-only bundled root.
func (s *Store) pluginDir(id string) (string, bool, error) {
	if err := ValidateID(id); err != nil {
		return "", false, err
	}
	user := filepath.Join(s.root, id, "plugin.json")
	if _, err := os.Stat(user); err == nil {
		return filepath.Join(s.root, id), false, nil
	}
	if s.builtin != "" {
		builtin := filepath.Join(s.builtin, id, "plugin.json")
		if _, err := os.Stat(builtin); err == nil {
			return filepath.Join(s.builtin, id), true, nil
		}
	}
	return "", false, fmt.Errorf("plugins: plugin %q is not installed", id)
}

func parseManifest(id string, raw []byte) (*Manifest, error) {
	if len(raw) > maxPluginManifestBytes {
		return nil, fmt.Errorf(
			"plugins: manifest %q exceeds %d bytes", id, maxPluginManifestBytes)
	}
	var m Manifest
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
	if err := validateVersion(m.Version); err != nil {
		return nil, fmt.Errorf("plugins: version: %w", err)
	}
	if m.MinHostVersion != "" {
		if err := validateVersion(m.MinHostVersion); err != nil {
			return nil, fmt.Errorf("plugins: minHostVersion: %w", err)
		}
	}
	if err := validateUpdateSource(m.Update); err != nil {
		return nil, err
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
	if err := validateAgentCapabilities(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateAgentCapabilities checks the skills / MCP / hooks / tools
// declarations: each group requires its permission, paths must stay
// inside the plugin directory, and tool/MCP entries must satisfy the
// host contract.
func validateAgentCapabilities(m *Manifest) error {
	if len(m.Skills) > 0 {
		if err := requirePermission(m, "skills:contribute", "skills"); err != nil {
			return err
		}
		if len(m.Skills) > maxPluginSkillCount {
			return fmt.Errorf("plugins: skills exceed %d entries", maxPluginSkillCount)
		}
		for i, p := range m.Skills {
			if err := validateRelativePluginPath(p); err != nil {
				return fmt.Errorf("plugins: skills[%d]: %w", i, err)
			}
			if len(p) > maxPluginPathLen {
				return fmt.Errorf("plugins: skills[%d]: path exceeds %d bytes", i, maxPluginPathLen)
			}
		}
	}
	if len(m.Hooks) > 0 {
		if err := requirePermission(m, "hooks:register", "hooks"); err != nil {
			return err
		}
		if len(m.Hooks) > maxPluginHookCount {
			return fmt.Errorf("plugins: hooks exceed %d entries", maxPluginHookCount)
		}
		for i, p := range m.Hooks {
			if err := validateRelativePluginPath(p); err != nil {
				return fmt.Errorf("plugins: hooks[%d]: %w", i, err)
			}
			if len(p) > maxPluginPathLen {
				return fmt.Errorf("plugins: hooks[%d]: path exceeds %d bytes", i, maxPluginPathLen)
			}
		}
	}
	if len(m.McpServers) > 0 {
		if err := requirePermission(m, "mcp:contribute", "mcpServers"); err != nil {
			return err
		}
		if len(m.McpServers) > maxPluginMCPServerCount {
			return fmt.Errorf("plugins: mcpServers exceed %d entries", maxPluginMCPServerCount)
		}
		seen := map[string]bool{}
		for i, srv := range m.McpServers {
			if !toolNameRe.MatchString(srv.Name) || seen[srv.Name] {
				return fmt.Errorf("plugins: duplicate or empty mcp server name %q", srv.Name)
			}
			seen[srv.Name] = true
			if len(srv.Command) > maxPluginCommandLen {
				return fmt.Errorf("plugins: mcpServers[%d] (%s): command exceeds %d bytes",
					i, srv.Name, maxPluginCommandLen)
			}
			if len(srv.URL) > maxPluginURLLen {
				return fmt.Errorf("plugins: mcpServers[%d] (%s): url exceeds %d bytes",
					i, srv.Name, maxPluginURLLen)
			}
			if len(srv.Env) > maxPluginEnvCount {
				return fmt.Errorf("plugins: mcpServers[%d] (%s): env exceeds %d entries",
					i, srv.Name, maxPluginEnvCount)
			}
			for k, v := range srv.Env {
				if len(k) > maxPluginEnvKeyLen || len(v) > maxPluginEnvValueLen {
					return fmt.Errorf("plugins: mcpServers[%d] (%s): env entry %q is too large",
						i, srv.Name, k)
				}
			}
			switch srv.Transport {
			case "stdio":
				if strings.TrimSpace(srv.Command) == "" {
					return fmt.Errorf("plugins: mcpServers[%d] (%s): stdio requires command", i, srv.Name)
				}
				if srv.URL != "" {
					return fmt.Errorf("plugins: mcpServers[%d] (%s): url is an http field", i, srv.Name)
				}
			case "http":
				if strings.TrimSpace(srv.URL) == "" {
					return fmt.Errorf("plugins: mcpServers[%d] (%s): http requires url", i, srv.Name)
				}
				if srv.Command != "" {
					return fmt.Errorf("plugins: mcpServers[%d] (%s): command is a stdio field", i, srv.Name)
				}
			default:
				return fmt.Errorf(
					"plugins: mcpServers[%d] (%s): unknown transport %q (want stdio | http)",
					i, srv.Name, srv.Transport)
			}
		}
	}
	if len(m.Tools) > 0 {
		if m.Capability == nil {
			return fmt.Errorf("plugins: tools require a capability subprocess")
		}
		if err := requirePermission(m, "tools:expose", "tools"); err != nil {
			return err
		}
		if len(m.Tools) > maxPluginToolCount {
			return fmt.Errorf("plugins: tools exceed %d entries", maxPluginToolCount)
		}
		seen := map[string]bool{}
		for i := range m.Tools {
			t := &m.Tools[i]
			if len(t.InputSchema) == 0 {
				t.InputSchema = json.RawMessage(`{"type":"object"}`)
			}
			if !toolNameRe.MatchString(t.Name) || seen[t.Name] {
				return fmt.Errorf("plugins: duplicate or invalid tool name %q", t.Name)
			}
			seen[t.Name] = true
			if strings.TrimSpace(t.Method) == "" {
				return fmt.Errorf("plugins: tools[%d] (%s): method is required", i, t.Name)
			}
			if len(t.Method) > maxPluginMethodLen {
				return fmt.Errorf("plugins: tools[%d] (%s): method exceeds %d bytes",
					i, t.Name, maxPluginMethodLen)
			}
			if utf8.RuneCountInString(t.Description) > maxPluginDescriptionChars {
				return fmt.Errorf("plugins: tools[%d] (%s): description exceeds %d characters",
					i, t.Name, maxPluginDescriptionChars)
			}
			if len(t.InputSchema) > maxPluginInputSchemaBytes {
				return fmt.Errorf("plugins: tools[%d] (%s): inputSchema exceeds %d bytes",
					i, t.Name, maxPluginInputSchemaBytes)
			}
			var probe map[string]any
			if !json.Valid(t.InputSchema) ||
				json.Unmarshal(t.InputSchema, &probe) != nil ||
				probe == nil {
				return fmt.Errorf("plugins: tools[%d] (%s): inputSchema must be a JSON object", i, t.Name)
			}
		}
	}
	return nil
}

// validateUpdateSource checks the optional update endpoint: http(s),
// no credentials embedded, no fragment, bounded length.
func validateUpdateSource(u *PluginUpdateSource) error {
	if u == nil {
		return nil
	}
	if strings.TrimSpace(u.URL) == "" {
		return fmt.Errorf("plugins: update.url is required when update is declared")
	}
	if len(u.URL) > maxPluginURLLen {
		return fmt.Errorf("plugins: update.url exceeds %d bytes", maxPluginURLLen)
	}
	parsed, err := url.Parse(u.URL)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return fmt.Errorf(
			"plugins: update.url %q must be an absolute http(s) URL without credentials or fragment",
			u.URL)
	}
	return nil
}

func requirePermission(m *Manifest, perm, what string) error {
	for _, p := range m.Permissions {
		if p == perm {
			return nil
		}
	}
	return fmt.Errorf("plugins: %s require permission %q", what, perm)
}

func manifestHasPermission(m *Manifest, perm string) bool {
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// validateRelativePluginPath requires a non-empty path that stays
// lexically inside the plugin directory.
func validateRelativePluginPath(p string) error {
	clean := filepath.Clean(p)
	if strings.TrimSpace(p) == "" ||
		filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q must be relative to the plugin directory", p)
	}
	return nil
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
