package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins/kraft"
)

func writePlugin(t *testing.T, root, id string, m map[string]any) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "dist", "index.js"), []byte("export const apply = () => {}"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func TestHostExposesAgentCapabilities(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "cap", map[string]any{
		"id": "cap", "name": "Cap", "version": "0.1.0",
		"entry": "dist/index.js",
		"kraft": map[string]any{"binary": "bin/srv", "protocol": 1},
		"permissions": []string{
			"skills:provide", "mcp:provide", "hooks:provide", "tools:provide",
		},
		"skills":     []string{"skills"},
		"mcpServers": []any{map[string]any{"name": "srv", "transport": "stdio", "command": "bin/srv"}},
		"hooks":      []string{"hooks/hooks.json"},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "ping",
			"inputSchema": map[string]any{"type": "object"}, "mutatesState": false,
		}},
	})
	for _, rel := range []string{
		"skills/SKILL.md", "hooks/hooks.json", "bin/srv",
	} {
		p := filepath.Join(root, "cap", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	host := NewHost(context.Background(), plugins.NewStore(root), nil)
	roots := host.SkillRoots()
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Join(root, "cap", "skills") {
		t.Fatalf("SkillRoots = %v", roots)
	}
	hookSources := host.PluginHooks()
	if len(hookSources) != 1 ||
		hookSources[0].Path != filepath.Join(root, "cap", "hooks", "hooks.json") ||
		hookSources[0].Dir != filepath.Join(root, "cap") {
		t.Fatalf("PluginHooks = %+v", hookSources)
	}
	servers := host.MCPServers()
	if len(servers) != 1 ||
		servers[0].Command != filepath.Join(root, "cap", "bin", "srv") ||
		servers[0].Prefix != "cap__srv__" {
		t.Fatalf("MCPServers = %+v", servers)
	}
	specs := host.ToolSpecs()
	if len(specs) != 1 || specs[0].Name != "ping" || specs[0].MutatesState {
		t.Fatalf("ToolSpecs = %+v", specs)
	}
	if _, err := host.Invoke(t.Context(), "cap", "ping", json.RawMessage(`{}`)); err == nil {
		t.Fatal("Invoke without a kraft runtime must fail")
	}
}

func TestHostDefaultSkillRoot(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "skills-only", map[string]any{
		"id": "skills-only", "name": "Skills", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"skills:provide"},
	})
	skillDir := filepath.Join(root, "skills-only", "skills")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := NewHost(
		context.Background(), plugins.NewStore(root), nil,
	).SkillRoots()
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(skillDir) {
		t.Fatalf("SkillRoots = %v, want default skills dir", roots)
	}
}

// TestHostEntriesCacheFrozenUntilNewHost pins the disk half of the
// cache contract: the scan is keyed by the registry revision, and a
// plugin that merely appears on disk (outside the Store API) moves
// nothing — it stays invisible to a live Host, and only the rebuild
// that constructs a fresh Host (SetAgentPlugins →
// engine.WithAgentPlugins) picks it up.
func TestHostEntriesCacheFrozenUntilNewHost(t *testing.T) {
	root := t.TempDir()
	store := plugins.NewStore(root)
	host := NewHost(context.Background(), store, nil)

	// Prime the cache while the store is empty.
	if roots := host.SkillRoots(); len(roots) != 0 {
		t.Fatalf("SkillRoots before install = %v, want empty", roots)
	}

	writePlugin(t, root, "late", map[string]any{
		"id": "late", "name": "Late", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"skills:provide"},
	})
	skillDir := filepath.Join(root, "late", "skills")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The already-cached Host must not see the late plugin.
	if roots := host.SkillRoots(); len(roots) != 0 {
		t.Fatalf("cached Host SkillRoots = %v, want empty (cache is frozen)", roots)
	}

	// A fresh Host (what a rebuild produces) rescans and sees it.
	fresh := NewHost(context.Background(), store, nil)
	roots := fresh.SkillRoots()
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(skillDir) {
		t.Fatalf("fresh Host SkillRoots = %v, want the late plugin skills dir", roots)
	}
}

// TestHostRescansWhenRegistryRevisionMoves pins the other half: a
// registry mutation moves Store's revision, and an already-live Host
// re-scans on its next read instead of waiting for a new assembly. That
// is what makes a change made mid-turn visible to the rest of the turn
// (the agent's own plugin_install defers the runtime reload to the end
// of the calling turn, and this Host keeps serving it).
func TestHostRescansWhenRegistryRevisionMoves(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "rev", map[string]any{
		"id": "rev", "name": "Rev", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"skills:provide"},
	})
	skillDir := filepath.Join(root, "rev", "skills")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	store := plugins.NewStore(root)
	host := NewHost(context.Background(), store, nil)
	if roots := host.SkillRoots(); len(roots) != 1 {
		t.Fatalf("SkillRoots before disable = %v, want the plugin root", roots)
	}

	if err := store.SetEnabled("rev", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if roots := host.SkillRoots(); len(roots) != 0 {
		t.Fatalf("SkillRoots after disable = %v, want empty (same Host)", roots)
	}

	if err := store.SetEnabled("rev", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	roots := host.SkillRoots()
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(skillDir) {
		t.Fatalf("SkillRoots after re-enable = %v, want %s", roots, skillDir)
	}
}

func TestHostMCPCommandResolution(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "mcp", map[string]any{
		"id": "mcp", "name": "MCP", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"mcp:provide"},
		"mcpServers": []any{
			map[string]any{"name": "local", "transport": "stdio", "command": "bin/srv"},
			map[string]any{"name": "path", "transport": "stdio", "command": "npx"},
		},
	})
	servers := NewHost(
		context.Background(), plugins.NewStore(root), nil,
	).MCPServers()
	if len(servers) != 2 {
		t.Fatalf("MCPServers = %+v", servers)
	}
	if servers[0].Command != filepath.Join(root, "mcp", "bin", "srv") {
		t.Fatalf("local command = %q, want plugin-relative resolution", servers[0].Command)
	}
	if servers[1].Command != "npx" {
		t.Fatalf("path command = %q, want bare PATH command untouched", servers[1].Command)
	}
}

func TestHostMCPServerPrefixSanitizesPluginID(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "my.plugin", map[string]any{
		"id": "my.plugin", "name": "Dotted", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"mcp:provide"},
		"mcpServers": []any{map[string]any{
			"name": "srv", "transport": "stdio", "command": "bin/srv",
		}},
	})
	if err := os.MkdirAll(filepath.Join(root, "my.plugin", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "my.plugin", "bin", "srv"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	servers := NewHost(
		context.Background(), plugins.NewStore(root), nil,
	).MCPServers()
	if len(servers) != 1 || servers[0].Prefix != "my_plugin__srv__" {
		t.Fatalf("MCPServers = %+v, want sanitized prefix", servers)
	}
}

// TestResolveInside pins the containment rule for plugin
// manifest-declared paths: staying inside the plugin directory is
// allowed, and "../", absolute paths and symlinks out of it are not —
// including a sibling directory whose name merely starts with the
// plugin directory's.
func TestResolveInside(t *testing.T) {
	root := t.TempDir()
	sibling := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-evil")
	t.Cleanup(func() {
		if err := os.RemoveAll(sibling); err != nil {
			t.Errorf("remove sibling: %v", err)
		}
	})
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sibling, "out.txt"), []byte("x"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(root, "sub"), filepath.Join(root, "inward"),
	); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(sibling, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	cases := []struct {
		rel  string
		want bool
	}{
		{"sub/new.txt", true},     // missing leaf under a real directory
		{"inward/new.txt", true},  // symlink that stays inside
		{"../out.txt", false},     // lexical escape
		{"/etc/hosts", false},     // absolute path
		{"escape/out.txt", false}, // existing file behind the symlink
		{"escape/new.txt", false}, // missing leaf behind the symlink
	}
	for _, tc := range cases {
		got, ok := resolveInside(root, tc.rel)
		if ok != tc.want {
			t.Errorf("resolveInside(%q) = %q, %v; want ok=%v",
				tc.rel, got, ok, tc.want)
		}
	}
}

// writeToolPlugin installs one plugin that exposes one kraft tool. The
// declared binary is a stub that exits immediately: these tests are
// about the gate, which sits in front of the kraft runtime.
func writeToolPlugin(t *testing.T, root, id string) {
	t.Helper()
	writePlugin(t, root, id, map[string]any{
		"id": id, "name": id, "version": "0.1.0",
		"entry":       "dist/index.js",
		"kraft":       map[string]any{"binary": "bin/srv", "protocol": 1},
		"permissions": []string{"tools:provide"},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "ping",
			"inputSchema": map[string]any{"type": "object"},
		}},
	})
	if err := os.MkdirAll(filepath.Join(root, id, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, id, "bin", "srv"), []byte("#!/bin/sh\nexit 1\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
}

// gateErr reports whether err is the gate refusing a method, as opposed
// to the kraft runtime failing once the gate let the call through.
func gateErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "does not expose tool method")
}

// TestHostInvokeGateFollowsTheLiveRegistry executes the gate both ways.
// Nothing did before: the only direct Invoke caller built a host with a
// nil kraft and returned at the nil check, so reverting the plugin scan
// to a sync.Once, or deleting the gate loop, left the suite green. The
// gate is the one part of the agent face that follows the registry
// immediately — a method a stopped or disabled plugin declares must stop
// being callable before the turn ends.
func TestHostInvokeGateFollowsTheLiveRegistry(t *testing.T) {
	root := t.TempDir()
	writeToolPlugin(t, root, "cap")
	store := plugins.NewStore(root)
	mgr := kraft.NewManager(root, kraft.DefaultLoader{
		Root:            root,
		KraftFunc:       store.Kraft,
		DirFunc:         store.Dir,
		PermissionsFunc: store.Permissions,
	}, nil)
	defer mgr.Shutdown()
	host := NewHost(t.Context(), store, mgr)
	ctx := t.Context()

	// A declared method gets past the gate and into the kraft runtime,
	// whose stub binary cannot serve it.
	if _, err := host.Invoke(ctx, "cap", "ping", json.RawMessage(`{}`)); gateErr(err) {
		t.Fatalf("declared method was refused by the gate: %v", err)
	}
	// An undeclared method never reaches the runtime.
	if _, err := host.Invoke(ctx, "cap", "nope", json.RawMessage(`{}`)); !gateErr(err) {
		t.Fatalf("undeclared method error = %v, want the gate refusal", err)
	}

	// Disabling the plugin closes the gate without a new Host and
	// without an assembly.
	if err := store.SetEnabled("cap", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := host.Invoke(ctx, "cap", "ping", json.RawMessage(`{}`)); !gateErr(err) {
		t.Fatalf("gate after disable = %v, want the gate refusal", err)
	}

	// Re-enabling it reopens the gate.
	if err := store.SetEnabled("cap", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := host.Invoke(ctx, "cap", "ping", json.RawMessage(`{}`)); gateErr(err) {
		t.Fatalf("declared method after re-enable was refused: %v", err)
	}

	// An install that lands mid-turn is callable in the same turn: the
	// runtime swap waits for the drain, the gate does not.
	late := t.TempDir()
	writeToolPlugin(t, late, "late")
	if _, err := store.Install(filepath.Join(late, "late")); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := host.Invoke(ctx, "late", "ping", json.RawMessage(`{}`)); gateErr(err) {
		t.Fatalf("freshly installed method was refused by the gate: %v", err)
	}

	// Uninstalling it closes the gate again.
	if err := store.Uninstall("late"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := host.Invoke(ctx, "late", "ping", json.RawMessage(`{}`)); !gateErr(err) {
		t.Fatalf("gate after uninstall = %v, want the gate refusal", err)
	}

	// The gate keys on the method, not on the tool's name: a manifest
	// may expose a tool called "ping" whose method is "system.ping", and
	// only the method is callable. An index built from names would pass
	// the second call and refuse the first.
	aliased := t.TempDir()
	writePlugin(t, aliased, "aliased", map[string]any{
		"id": "aliased", "name": "aliased", "version": "0.1.0",
		"entry":       "dist/index.js",
		"kraft":       map[string]any{"binary": "bin/srv", "protocol": 1},
		"permissions": []string{"tools:provide"},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "system.ping",
			"inputSchema": map[string]any{"type": "object"},
		}},
	})
	if err := os.MkdirAll(filepath.Join(aliased, "aliased", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(aliased, "aliased", "bin", "srv"),
		[]byte("#!/bin/sh\nexit 1\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Install(filepath.Join(aliased, "aliased")); err != nil {
		t.Fatalf("install aliased: %v", err)
	}
	if _, err := host.Invoke(ctx, "aliased", "system.ping", json.RawMessage(`{}`)); gateErr(err) {
		t.Fatalf("declared method was refused by the gate: %v", err)
	}
	if _, err := host.Invoke(ctx, "aliased", "ping", json.RawMessage(`{}`)); !gateErr(err) {
		t.Fatalf("the tool name was callable as a method: %v", err)
	}
}

// TestHostWatchFollowsRegistryMutations pins the push side of the clock
// for the agent face: a subscriber wakes once per effective mutation and
// stops when its cancel runs. A host without a registry stays
// unconditional.
func TestHostWatchFollowsRegistryMutations(t *testing.T) {
	root := t.TempDir()
	writeToolPlugin(t, root, "cap")
	store := plugins.NewStore(root)
	host := NewHost(t.Context(), store, nil)

	var calls atomic.Int64
	cancel := host.Watch(func() { calls.Add(1) })
	if err := store.SetEnabled("cap", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("watch calls after disable = %d, want 1", got)
	}
	if err := store.SetEnabled("cap", false); err != nil {
		t.Fatalf("repeat disable: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("watch calls after a no-op = %d, want 1", got)
	}
	cancel()
	if err := store.SetEnabled("cap", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("watch calls after cancel = %d, want 1", got)
	}

	// NewEmpty has no registry; watching it must be a no-op, not a nil
	// function the caller has to remember to check.
	stop := NewEmpty().Watch(func() { t.Error("a host with no registry woke a watcher") })
	if stop == nil {
		t.Fatal("Watch must return a cancel even without a registry")
	}
	stop()
}
