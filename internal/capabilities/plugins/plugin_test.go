package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writePlugin(t *testing.T, root, id string, m map[string]any, bundle string) {
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
	if bundle != "" {
		entry := m["entry"].(string)
		if err := os.WriteFile(filepath.Join(dir, entry), []byte(bundle), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreListScansAndValidates(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
		"contributes": map[string]any{
			"settingsPanels": []any{
				map[string]any{"id": "hello-panel", "title": "Hello", "order": 10},
			},
		},
	}, "console.log('hi')")
	writePlugin(t, root, "bad-perm", map[string]any{
		"id": "bad-perm", "name": "Bad", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"unknown:perm"},
	}, "")
	writePlugin(t, root, "bad-id", map[string]any{
		"id": "mismatch", "name": "Bad", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}

	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List returned %d plugins, want 3: %+v", len(list), list)
	}
	byID := map[string]PluginSummary{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if h := byID["hello"]; !h.Enabled || h.Error != "" || len(h.Panels) != 1 || h.Panels[0] != "hello-panel" {
		t.Fatalf("hello summary = %+v", h)
	}
	if b := byID["bad-perm"]; b.Error == "" {
		t.Fatal("bad-perm should be rejected")
	}
	if b := byID["bad-id"]; b.Error == "" {
		t.Fatal("bad-id should be rejected")
	}
}

func TestStoreBundleValidatesPath(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "console.log('hello')")
	s := NewStore(root)
	src, err := s.Bundle("hello")
	if err != nil || src != "console.log('hello')" {
		t.Fatalf("Bundle = (%q, %v)", src, err)
	}
	writePlugin(t, root, "evil", map[string]any{
		"id": "evil", "name": "Evil", "version": "0.1.0",
		"entry": "../outside.js", "permissions": []string{},
	}, "")
	if _, err := s.Bundle("evil"); err == nil {
		t.Fatal("escaping entry should fail")
	}
	if _, err := s.Bundle("../hello"); err == nil {
		t.Fatal("invalid id should fail")
	}
}

func TestStoreSetEnabledTogglesState(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	s := NewStore(root)
	if err := s.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("plugin should be disabled: %+v", list)
	}
	if err := s.SetEnabled("hello", true); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if !list[0].Enabled {
		t.Fatal("plugin should be enabled again")
	}
	if err := s.SetEnabled("missing", true); err == nil {
		t.Fatal("enabling a non-installed plugin should fail")
	}
}

func TestStoreInstallCopiesAndValidates(t *testing.T) {
	root := t.TempDir()
	srcRoot := t.TempDir()
	writePlugin(t, srcRoot, "installed", map[string]any{
		"id": "installed", "name": "Installed", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
		"contributes": map[string]any{
			"sidebarEntries": []any{
				map[string]any{"id": "inst-entry", "title": "Inst", "order": 1},
			},
		},
	}, "console.log('installed')")
	src := filepath.Join(srcRoot, "unrelated-dir-name")
	if err := os.Rename(filepath.Join(srcRoot, "installed"), src); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)
	sum, err := s.Install(src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if sum.ID != "installed" || !sum.Enabled || len(sum.Entries) != 1 {
		t.Fatalf("installed summary = %+v", sum)
	}
	if _, err := s.Bundle("installed"); err != nil {
		t.Fatalf("bundle after install: %v", err)
	}
	if _, err := s.Install(src); err == nil {
		t.Fatal("reinstalling an existing plugin should fail")
	}
	bad := t.TempDir()
	writePlugin(t, bad, "x", map[string]any{
		"id": "bad", "name": "Bad", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"nope:perm"},
	}, "")
	if _, err := s.Install(filepath.Join(bad, "x")); err == nil {
		t.Fatal("installing a plugin with unknown permissions should fail")
	}
}

func TestStoreUninstallRemoves(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	s := NewStore(root)
	if err := s.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall("hello"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("plugins after uninstall = %+v", list)
	}
	if err := s.Uninstall("missing"); err == nil {
		t.Fatal("uninstalling a non-installed plugin should fail")
	}
}

func TestInstallMakesCapabilityExecutable(t *testing.T) {
	srcRoot := t.TempDir()
	writePlugin(t, srcRoot, "cap", map[string]any{
		"id": "cap", "name": "Cap", "version": "1.0.0",
		"entry":      "dist/index.js",
		"capability": map[string]any{"binary": "bin/auth", "protocol": 1},
	}, "export function apply() {}")
	bin := filepath.Join(srcRoot, "cap", "bin", "auth")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	store := NewStore(root)
	if _, err := store.Install(filepath.Join(srcRoot, "cap")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "cap", "bin", "auth"))
	if err != nil {
		t.Fatal(err)
	}
	// copyDir writes everything 0600; Install must restore the exec bit
	// for the declared capability binary.
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("capability binary is not executable after install: %v", info.Mode())
	}
}

func TestManifestValidatesAgentCapabilities(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "agent", map[string]any{
		"id": "agent", "name": "Agent", "version": "0.1.0",
		"entry":      "dist/index.js",
		"capability": map[string]any{"binary": "bin/agent", "protocol": 1},
		"permissions": []string{
			"skills:contribute", "mcp:contribute", "hooks:register", "tools:expose",
		},
		"update":     map[string]any{"url": "https://example.com/plugin/latest.json"},
		"skills":     []string{"skills"},
		"mcpServers": []any{map[string]any{"name": "srv", "transport": "stdio", "command": "bin/srv"}},
		"hooks":      []string{"hooks/hooks.json"},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "ping",
			"inputSchema": map[string]any{"type": "object"},
		}},
	}, "")
	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List = %+v, want one valid plugin", list)
	}
	p := list[0]
	if p.Error != "" || !p.HasSkills || !p.HasMCP || !p.HasHooks || !p.HasTools || !p.HasUpdate {
		t.Fatalf("agent plugin summary = %+v", p)
	}
}

func TestManifestRejectsAgentCapabilityMistakes(t *testing.T) {
	cases := []struct {
		name string
		m    map[string]any
	}{
		{
			name: "tools without permission",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"capability": map[string]any{"binary": "bin/x", "protocol": 1},
				"tools":      []any{map[string]any{"name": "t", "method": "m"}},
			},
		},
		{
			name: "tools without capability",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"tools:expose"},
				"tools":       []any{map[string]any{"name": "t", "method": "m"}},
			},
		},
		{
			name: "mcp unknown transport",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"mcp:contribute"},
				"mcpServers":  []any{map[string]any{"name": "s", "transport": "carrier"}},
			},
		},
		{
			name: "skill path escapes",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"skills:contribute"},
				"skills":      []string{"../skills"},
			},
		},
		{
			name: "tool schema not object",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"tools:expose"},
				"tools": []any{map[string]any{
					"name": "t", "method": "m", "inputSchema": []string{"not", "object"},
				}},
			},
		},
		{
			name: "mcp server name with separator",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"mcp:contribute"},
				"mcpServers":  []any{map[string]any{"name": "bad:name", "transport": "stdio", "command": "bin/s"}},
			},
		},
		{
			name: "tool name with dot",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"capability":  map[string]any{"binary": "bin/x", "protocol": 1},
				"permissions": []string{"tools:expose"},
				"tools": []any{map[string]any{
					"name": "tool.name", "method": "m",
				}},
			},
		},
		{
			name: "mcp server name with dot",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"mcp:contribute"},
				"mcpServers":  []any{map[string]any{"name": "bad.name", "transport": "stdio", "command": "bin/s"}},
			},
		},
		{
			name: "tool description too long",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"capability":  map[string]any{"binary": "bin/x", "protocol": 1},
				"permissions": []string{"tools:expose"},
				"tools": []any{map[string]any{
					"name": "t", "method": "m",
					"description": strings.Repeat("x", 1025),
				}},
			},
		},
		{
			name: "tool schema too large",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"capability":  map[string]any{"binary": "bin/x", "protocol": 1},
				"permissions": []string{"tools:expose"},
				"tools": []any{map[string]any{
					"name": "t", "method": "m",
					"inputSchema": map[string]any{
						"padding": strings.Repeat("x", 33<<10),
					},
				}},
			},
		},
		{
			name: "invalid version format",
			m: map[string]any{
				"id": "x", "name": "X", "version": "abc",
				"entry": "dist/index.js", "permissions": []string{},
			},
		},
		{
			name: "update url ftp scheme",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0",
				"entry": "dist/index.js", "permissions": []string{},
				"update": map[string]any{"url": "ftp://example.com/latest.json"},
			},
		},
		{
			name: "update url with credentials",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0",
				"entry": "dist/index.js", "permissions": []string{},
				"update": map[string]any{"url": "https://user:pass@example.com/latest.json"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writePlugin(t, root, "x", tc.m, "")
			s := NewStore(root)
			list, err := s.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(list) != 1 || list[0].Error == "" {
				t.Fatalf("List = %+v, want a rejected plugin", list)
			}
		})
	}
}

func TestInstallRejectsMissingAgentResources(t *testing.T) {
	srcRoot := t.TempDir()
	writePlugin(t, srcRoot, "broken", map[string]any{
		"id": "broken", "name": "Broken", "version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": []string{
			"skills:contribute", "hooks:register", "mcp:contribute",
		},
		"skills":     []string{"skills"},
		"hooks":      []string{"hooks/hooks.json"},
		"mcpServers": []any{map[string]any{"name": "srv", "transport": "stdio", "command": "bin/srv"}},
	}, "")
	s := NewStore(t.TempDir())
	if _, err := s.Install(filepath.Join(srcRoot, "broken")); err == nil {
		t.Fatal("installing a plugin with missing declared resources must fail")
	}
}

func TestStoreUpdateRollsBackAndPreservesState(t *testing.T) {
	root := t.TempDir()
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "old-bundle")
	newSrc := t.TempDir()
	writePlugin(t, newSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "new-bundle")

	s := NewStore(root)
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := s.SetEnabled("p", false); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Update("p", filepath.Join(newSrc, "p"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if sum.Version != "0.2.0" || sum.Enabled {
		t.Fatalf("updated summary = %+v, want 0.2.0 and disabled preserved", sum)
	}
	if bundle, _ := s.Bundle("p"); bundle != "new-bundle" {
		t.Fatalf("bundle after update = %q", bundle)
	}
	if _, err := s.Update("p", filepath.Join(oldSrc, "p")); err == nil {
		t.Fatal("downgrade/equal version update must be rejected")
	}
	list, _ := s.List()
	if len(list) != 1 || !list[0].CanRollback {
		t.Fatalf("list after update = %+v, want rollback available", list)
	}

	rb, err := s.Rollback("p")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rb.Version != "0.1.0" || rb.Enabled {
		t.Fatalf("rollback summary = %+v", rb)
	}
	if bundle, _ := s.Bundle("p"); bundle != "old-bundle" {
		t.Fatalf("bundle after rollback = %q", bundle)
	}
	list, _ = s.List()
	if len(list) != 1 || list[0].CanRollback {
		t.Fatalf("list after rollback = %+v, want no rollback snapshot", list)
	}
}

func TestStoreUpdateZip(t *testing.T) {
	root := t.TempDir()
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "old")
	zipPath := writeTestZip(t, map[string]string{
		"plugin.json":   `{"id":"p","name":"P","version":"0.2.0","entry":"dist/index.js","permissions":[]}`,
		"dist/index.js": "new",
	})
	s := NewStore(root)
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateZip("p", zipPath); err != nil {
		t.Fatalf("UpdateZip: %v", err)
	}
	if bundle, _ := s.Bundle("p"); bundle != "new" {
		t.Fatalf("bundle after UpdateZip = %q", bundle)
	}
}

func TestStoreEnforcesMinHostVersion(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	writePlugin(t, src, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"minHostVersion": "0.3.0", "entry": "dist/index.js",
		"permissions": []string{},
	}, "")
	s := NewStore(root)
	s.SetHostVersion("0.2.0")
	if _, err := s.Install(filepath.Join(src, "p")); err == nil {
		t.Fatal("install with minHostVersion above host must fail")
	}
}

func TestStoreUpdateRejectsMinHostVersion(t *testing.T) {
	root := t.TempDir()
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	newSrc := t.TempDir()
	writePlugin(t, newSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"minHostVersion": "0.9.0", "entry": "dist/index.js",
		"permissions": []string{},
	}, "")
	s := NewStore(root)
	s.SetHostVersion("0.5.0")
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("p", filepath.Join(newSrc, "p")); err == nil {
		t.Fatal("update with minHostVersion above host must fail")
	}
}

func TestRollbackRestoresWhenCurrentMissing(t *testing.T) {
	root := t.TempDir()
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "old")
	newSrc := t.TempDir()
	writePlugin(t, newSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "new")
	s := NewStore(root)
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("p", filepath.Join(newSrc, "p")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "p")); err != nil {
		t.Fatal(err)
	}
	sum, err := s.Rollback("p")
	if err != nil {
		t.Fatalf("rollback with missing current dir: %v", err)
	}
	if sum.Version != "0.1.0" {
		t.Fatalf("rollback version = %q", sum.Version)
	}
	if bundle, _ := s.Bundle("p"); bundle != "old" {
		t.Fatalf("bundle after rollback = %q", bundle)
	}
}

func TestRollbackRejectsTamperedBackup(t *testing.T) {
	root := t.TempDir()
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry":       "dist/index.js",
		"permissions": []string{"skills:contribute"},
		"skills":      []string{"skills"},
	}, "")
	if err := os.MkdirAll(filepath.Join(oldSrc, "p", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	newSrc := t.TempDir()
	writePlugin(t, newSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	s := NewStore(root)
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("p", filepath.Join(newSrc, "p")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".backups", "p", "skills")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rollback("p"); err == nil {
		t.Fatal("rollback of a tampered backup must fail validation")
	}
}

func TestCompareVersionsSemverPrecedence(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.0-beta", 1},
		{"1.0.0-beta", "1.0.0-rc.1", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
	}
	for _, tc := range cases {
		got, err := compareVersions(tc.a, tc.b)
		if err != nil {
			t.Fatalf("compareVersions(%q, %q): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if _, err := compareVersions("abc", "1.0.0"); err == nil {
		t.Fatal("invalid version must be rejected")
	}
}

func TestConcurrentUpdateIsSerialized(t *testing.T) {
	root := t.TempDir()
	oldSrc := t.TempDir()
	writePlugin(t, oldSrc, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "old")
	newA := t.TempDir()
	writePlugin(t, newA, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "new-a")
	newB := t.TempDir()
	writePlugin(t, newB, "p", map[string]any{
		"id": "p", "name": "P", "version": "0.2.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "new-b")
	s := NewStore(root)
	if _, err := s.Install(filepath.Join(oldSrc, "p")); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, src := range []string{filepath.Join(newA, "p"), filepath.Join(newB, "p")} {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			_, err := s.Update("p", src)
			results <- err
		}(src)
	}
	wg.Wait()
	close(results)
	success := 0
	failures := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			failures++
		}
	}
	if success != 1 || failures != 1 {
		t.Fatalf("concurrent updates: success=%d failures=%d, want 1/1", success, failures)
	}
}
