// Package agent adapts the plugin registry to the agent runtime: it
// exposes the skills roots, MCP servers, lifecycle hooks and
// capability subprocess tools that enabled plugins contribute. The
// host remains semantic-agnostic; it only translates plugin manifests
// into the resource shapes the rest of the runtime consumes.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
)

// ResourceKind is the deploy resource kind of the shared plugin host.
const ResourceKind = "opencraft.plugins"

// Host is the read-side view of the plugin registry for agent-facing
// capabilities. A nil store yields an empty host (CLI / tests without
// a desktop plugin root).
type Host struct {
	ctx    context.Context
	store  *plugins.Store
	cap    *runtime.Manager
	once   sync.Once
	cached []pluginEntry
}

// NewHost wraps an installed plugin store and its capability runtime.
func NewHost(ctx context.Context, store *plugins.Store, cap *runtime.Manager) *Host {
	return &Host{ctx: ctx, store: store, cap: cap}
}

// NewEmpty returns a host with no plugins. It is used by runtimes
// that did not receive a desktop plugin root.
func NewEmpty() *Host { return &Host{} }

// Empty reports whether the host has no plugin store.
func (h *Host) Empty() bool { return h.store == nil }

// Factory builds the opencraft.plugins resource. Host may be nil for
// runtimes without a desktop plugin root; the resource still exists so
// skills/hooks/tool sources can declare an optional dependency on it.
type Factory struct {
	Host *Host
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "local"}
}

// New implements resource.Factory.
func (f Factory) New(_ context.Context, _ resource.Input) (any, error) {
	if f.Host == nil {
		return NewEmpty(), nil
	}
	return f.Host, nil
}

// ToolSpec is one agent-callable tool backed by a plugin capability
// subprocess method.
type ToolSpec struct {
	PluginID     string
	Name         string
	Description  string
	Method       string
	InputSchema  json.RawMessage
	MutatesState bool
}

// MCPServer is one plugin-contributed MCP server after path resolution.
type MCPServer struct {
	PluginID  string
	Name      string
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	// Prefix namespaces every tool exposed by this server.
	Prefix string
}

type pluginEntry struct {
	id  string
	dir string
	m   *plugins.Manifest
}

// entries returns every enabled, validly installed plugin.
func (h *Host) entries() []pluginEntry {
	h.once.Do(func() { h.cached = h.scanEntries() })
	return h.cached
}

func (h *Host) scanEntries() []pluginEntry {
	if h.store == nil {
		return nil
	}
	list, err := h.store.List()
	if err != nil {
		telemetry.WarnErr(h.ctx,
			"plugin agent: list plugins failed", err)
		return nil
	}
	var out []pluginEntry
	for _, p := range list {
		if !p.Enabled || p.Error != "" {
			continue
		}
		dir, _, err := h.store.Dir(p.ID)
		if err != nil {
			telemetry.WarnErr(h.ctx,
				"plugin agent: resolve plugin dir failed", err,
				otellog.String("plugin", p.ID))
			continue
		}
		m, err := h.store.Manifest(p.ID)
		if err != nil {
			telemetry.WarnErr(h.ctx,
				"plugin agent: read manifest failed", err,
				otellog.String("plugin", p.ID))
			continue
		}
		out = append(out, pluginEntry{id: p.ID, dir: dir, m: m})
	}
	return out
}

// SkillRoots returns the skill directories contributed by enabled
// plugins. A plugin without an explicit skills list contributes its
// default <root>/skills directory when it exists.
func (h *Host) SkillRoots() []string {
	var roots []string
	seen := map[string]bool{}
	for _, e := range h.entries() {
		if !hasPerm(e.m, "skills:contribute") {
			continue
		}
		var candidates []string
		if len(e.m.Skills) == 0 {
			candidates = []string{filepath.Join(e.dir, "skills")}
		} else {
			for _, rel := range e.m.Skills {
				if abs, ok := resolveInside(e.dir, rel); ok {
					candidates = append(candidates, abs)
				}
			}
		}
		for _, root := range candidates {
			if !isDir(root) {
				continue
			}
			clean := filepath.Clean(root)
			if !seen[clean] {
				seen[clean] = true
				roots = append(roots, clean)
			}
		}
	}
	return roots
}

// PluginHooks returns the hooks.json files contributed by enabled
// plugins. Dir anchors relative hook commands to the plugin root.
func (h *Host) PluginHooks() []hooks.ExtraSource {
	var out []hooks.ExtraSource
	for _, e := range h.entries() {
		if !hasPerm(e.m, "hooks:register") {
			continue
		}
		for _, rel := range e.m.Hooks {
			p, ok := resolveInside(e.dir, rel)
			if !ok {
				continue
			}
			out = append(out, hooks.ExtraSource{
				Path: p, Dir: e.dir, Trusted: false,
			})
		}
	}
	return out
}

// MCPServers returns the MCP servers contributed by enabled plugins.
// Relative stdio commands are resolved against the plugin directory.
func (h *Host) MCPServers() []MCPServer {
	var out []MCPServer
	for _, e := range h.entries() {
		if !hasPerm(e.m, "mcp:contribute") {
			continue
		}
		for _, srv := range e.m.McpServers {
			cmd := srv.Command
			if srv.Transport == "stdio" && cmd != "" && !filepath.IsAbs(cmd) &&
				(strings.Contains(cmd, "/") || strings.Contains(cmd, `\`) ||
					strings.HasPrefix(cmd, ".")) {
				if abs, ok := resolveInside(e.dir, cmd); ok {
					cmd = abs
				}
			}
			out = append(out, MCPServer{
				PluginID:  e.id,
				Name:      srv.Name,
				Transport: srv.Transport,
				Command:   cmd,
				Args:      append([]string(nil), srv.Args...),
				Env:       copyEnv(srv.Env),
				URL:       srv.URL,
				Prefix: strings.ReplaceAll(e.id, ".", "_") +
					"__" + srv.Name + "__",
			})
		}
	}
	return out
}

// ToolSpecs returns the capability subprocess tools declared by
// enabled plugins. MutatesState defaults to true.
func (h *Host) ToolSpecs() []ToolSpec {
	var out []ToolSpec
	for _, e := range h.entries() {
		if !hasPerm(e.m, "tools:expose") {
			continue
		}
		for _, t := range e.m.Tools {
			mutates := true
			if t.MutatesState != nil {
				mutates = *t.MutatesState
			}
			out = append(out, ToolSpec{
				PluginID:     e.id,
				Name:         t.Name,
				Description:  t.Description,
				Method:       t.Method,
				InputSchema:  append(json.RawMessage(nil), t.InputSchema...),
				MutatesState: mutates,
			})
		}
	}
	return out
}

// Invoke routes one agent tool call to a plugin capability
// subprocess. Only methods declared in the manifest are accepted.
func (h *Host) Invoke(
	ctx context.Context,
	pluginID, method string,
	args json.RawMessage,
) (json.RawMessage, error) {
	if h.cap == nil {
		return nil, fmt.Errorf("plugins: capability runtime is unavailable")
	}
	for _, spec := range h.ToolSpecs() {
		if spec.PluginID == pluginID && spec.Method == method {
			return h.cap.Invoke(ctx, pluginID, method, args)
		}
	}
	return nil, fmt.Errorf(
		"plugins: plugin %q does not expose tool method %q", pluginID, method)
}

func hasPerm(m *plugins.Manifest, perm string) bool {
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// resolveInside resolves a relative path against root, rejecting
// lexical escapes and symlinks that leave the root.
func resolveInside(root, rel string) (string, bool) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	abs := filepath.Join(root, clean)
	rootReal, rootErr := filepath.EvalSymlinks(root)
	absReal, absErr := filepath.EvalSymlinks(abs)
	if rootErr == nil && absErr == nil {
		if absReal != rootReal &&
			!strings.HasPrefix(absReal, rootReal+string(filepath.Separator)) {
			return "", false
		}
	}
	return filepath.Clean(abs), true
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func copyEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
