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
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/GizClaw/opencraft/internal/plugins/runtime"
)

// idRe constrains plugin/provider ids: lowercase start, then lowercase
// letters, digits, dot, underscore or dash.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// toolNameRe constrains agent-facing tool names: lowercase start,
// then lowercase letters, digits, dot, underscore or dash.
var toolNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

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
	// Agent-facing capability flags (skills / MCP / hooks / tools).
	HasSkills bool `json:"hasSkills,omitempty"`
	HasMCP    bool `json:"hasMcp,omitempty"`
	HasHooks  bool `json:"hasHooks,omitempty"`
	HasTools  bool `json:"hasTools,omitempty"`
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
	root    string
	builtin string
}

// NewStore returns a registry rooted at root.
func NewStore(root string) *Store {
	return &Store{root: root, builtin: runtime.BuiltinPluginRoot()}
}

// List returns every installed plugin with its manifest metadata and
// enabled state. A broken plugin is reported with its validation error
// instead of failing the whole list.
func (s *Store) List() ([]PluginSummary, error) {
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
	if err := validateInstalledAgentResources(dst, m); err != nil {
		_ = os.RemoveAll(dst)
		return PluginSummary{}, err
	}
	// Ensure the capability binary is executable regardless of how the
	// source was copied (permissions are not always preserved).
	if m.Capability != nil {
		bin := filepath.Join(dst, m.Capability.Binary)
		if err := os.Chmod(bin, 0o755); err != nil {
			_ = os.RemoveAll(dst)
			return PluginSummary{}, fmt.Errorf(
				"plugins: make capability binary executable: %w", err)
		}
		if err := signAdHoc(bin); err != nil {
			_ = os.RemoveAll(dst)
			return PluginSummary{}, err
		}
	}
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
	}
	if !sum.HasSkills && manifestHasPermission(m, "skills:contribute") &&
		dirExists(filepath.Join(dst, "skills")) {
		sum.HasSkills = true
	}
	for _, p := range m.Contributes.SettingsPanels {
		sum.Panels = append(sum.Panels, p.ID)
	}
	for _, e := range m.Contributes.SidebarEntries {
		sum.Entries = append(sum.Entries, e.ID)
	}
	return sum, nil
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
	return s.readManifest(id)
}

// Dir resolves the directory holding an installed plugin (user root
// wins over the builtin root). builtin reports whether the plugin is
// app-bundled and therefore read-only.
func (s *Store) Dir(id string) (string, bool, error) {
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
