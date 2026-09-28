// Package agent adapts the plugin registry to the agent runtime: it
// exposes the skills roots, MCP servers, lifecycle hooks and
// kraft tools that enabled plugins contribute. The
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

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// ToolSpec is one agent-callable tool backed by a method on a
// plugin's kraft.
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

// scan is one pass over the registry: the enabled plugins, plus the
// tool methods they expose, indexed so the Invoke gate is a map lookup
// rather than a rebuild of every tool definition. Both halves are cached
// together against the registry revision (see entries), because they
// come from the same manifests.
type scan struct {
	plugins []pluginEntry
	methods map[string]map[string]bool
}

// Watch registers fn to run after every plugin registry mutation, and
// returns a cancel that is safe to call more than once. It is the push
// side of the registry clock (see the charter's FaceRefreshes): a
// consumer that owns state it cannot rebuild on demand — the agent tool
// source, publishing into a live registry — subscribes instead of
// waiting for its next read. A host with no registry returns a no-op
// cancel, so callers stay unconditional.
func (h *Host) Watch(fn func()) (cancel func()) {
	if h.store == nil {
		return func() {}
	}
	return h.store.Subscribe(fn)
}

// entries returns every enabled, validly installed plugin, re-scanning
// whenever the registry revision moved since the last scan. An
// unchanged revision keeps the cached scan, so the common path stays
// one map lookup while a mid-flight registry change (for example an
// agent-authored install whose runtime reload is deferred to the end
// of the turn) reaches the next read.
func (h *Host) entries() scan {
	if h.store == nil {
		return scan{}
	}
	rev := h.store.Revision()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cachedSet && h.cachedRev == rev {
		return h.cached
	}
	h.cached = h.scanEntries()
	h.cachedRev = rev
	h.cachedSet = true
	return h.cached
}

func (h *Host) scanEntries() scan {
	if h.store == nil {
		return scan{}
	}
	// One scan: the store already parsed every manifest to build its
	// summaries, so the entries hand over the directory and the manifest
	// with them instead of this scan reading plugin.json again.
	entries, err := h.store.Entries()
	if err != nil {
		telemetry.WarnErr(h.ctx,
			"plugin agent: list plugins failed", err)
		return scan{}
	}
	out := scan{methods: map[string]map[string]bool{}}
	for _, e := range entries {
		if !e.Summary.Enabled || e.Summary.Error != "" || e.Manifest == nil {
			continue
		}
		out.plugins = append(out.plugins, pluginEntry{
			id: e.Summary.ID, dir: e.Dir, m: e.Manifest,
		})
		if hasPerm(e.Manifest, "tools:provide") {
			methods := make(map[string]bool, len(e.Manifest.Tools))
			for _, t := range e.Manifest.Tools {
				methods[t.Method] = true
			}
			out.methods[e.Summary.ID] = methods
		}
	}
	return out
}

// SkillRoots returns the skill directories contributed by enabled
// plugins. A plugin without an explicit skills list contributes its
// default <root>/skills directory when it exists.
func (h *Host) SkillRoots() []string {
	var roots []string
	seen := map[string]bool{}
	for _, e := range h.entries().plugins {
		if !hasPerm(e.m, "skills:provide") {
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
	for _, e := range h.entries().plugins {
		if !hasPerm(e.m, "hooks:provide") {
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
	for _, e := range h.entries().plugins {
		if !hasPerm(e.m, "mcp:provide") {
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

// ToolSpecs returns the kraft tools declared by enabled plugins.
// MutatesState defaults to true.
func (h *Host) ToolSpecs() []ToolSpec {
	var out []ToolSpec
	for _, e := range h.entries().plugins {
		if !hasPerm(e.m, "tools:provide") {
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

// Invoke routes one agent tool call to a plugin's kraft. Only methods
// declared in the manifest are accepted.
func (h *Host) Invoke(
	ctx context.Context,
	pluginID, method string,
	args json.RawMessage,
) (json.RawMessage, error) {
	if h.kraft == nil {
		return nil, fmt.Errorf("plugins: kraft runtime is unavailable")
	}
	// The gate reads the scan cached against the registry revision, so a
	// plugin disabled, updated or uninstalled mid-turn cannot be called
	// through a stale gate, and the common case costs one map lookup
	// instead of rebuilding every tool definition.
	if !h.entries().methods[pluginID][method] {
		return nil, fmt.Errorf(
			"plugins: plugin %q does not expose tool method %q", pluginID, method)
	}
	return h.kraft.Invoke(ctx, pluginID, method, args)
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
// lexical escapes and symlinks that leave the root. The containment
// check resolves through the longest existing ancestor, so a path
// that does not exist yet cannot hide behind a symlinked directory.
func resolveInside(root, rel string) (string, bool) {
	clean := filepath.Clean(rel)
	if !pathsafe.RelRef(clean) {
		return "", false
	}
	abs := filepath.Join(root, clean)
	if !pathsafe.RealWithin(root, abs) {
		return "", false
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
