package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/plugins"
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
		"entry":      "dist/index.js",
		"capability": map[string]any{"binary": "bin/srv", "protocol": 1},
		"permissions": []string{
			"skills:contribute", "mcp:contribute", "hooks:register", "tools:expose",
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

	host := NewHost(plugins.NewStore(root), nil)
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
		servers[0].Prefix != "cap:srv:" {
		t.Fatalf("MCPServers = %+v", servers)
	}
	specs := host.ToolSpecs()
	if len(specs) != 1 || specs[0].Name != "ping" || specs[0].MutatesState {
		t.Fatalf("ToolSpecs = %+v", specs)
	}
	if _, err := host.Invoke(t.Context(), "cap", "ping", json.RawMessage(`{}`)); err == nil {
		t.Fatal("Invoke without a capability runtime must fail")
	}
}

func TestHostDefaultSkillRoot(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "skills-only", map[string]any{
		"id": "skills-only", "name": "Skills", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"skills:contribute"},
	})
	skillDir := filepath.Join(root, "skills-only", "skills")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := NewHost(plugins.NewStore(root), nil).SkillRoots()
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(skillDir) {
		t.Fatalf("SkillRoots = %v, want default skills dir", roots)
	}
}

func TestHostMCPCommandResolution(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "mcp", map[string]any{
		"id": "mcp", "name": "MCP", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"mcp:contribute"},
		"mcpServers": []any{
			map[string]any{"name": "local", "transport": "stdio", "command": "bin/srv"},
			map[string]any{"name": "path", "transport": "stdio", "command": "npx"},
		},
	})
	servers := NewHost(plugins.NewStore(root), nil).MCPServers()
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
