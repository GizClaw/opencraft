package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if h := byID["hello"]; !h.Enabled || h.Error != "" {
		t.Fatalf("hello summary = %+v", h)
	}
	if b := byID["bad-perm"]; b.Error == "" {
		t.Fatal("bad-perm should be rejected")
	}
	if b := byID["bad-id"]; b.Error == "" {
		t.Fatal("bad-id should be rejected")
	}
}

// TestStoreEntriesCarryTheParsedManifest pins the scan both List and the
// agent host read: one pass parses every plugin.json, hands back the
// directory and the manifest with the summary, and reports an entry that
// does not parse as an error instead of a manifest. List is the same
// scan's summaries, so the two can never disagree about a plugin.
func TestStoreEntriesCarryTheParsedManifest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
		"kraft": map[string]any{"binary": "bin/srv", "protocol": 1},
	}, "console.log('hi')")
	writePlugin(t, root, "bad-perm", map[string]any{
		"id": "bad-perm", "name": "Bad", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"unknown:perm"},
	}, "")

	s := NewStore(root)
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Entries returned %d plugins, want 2: %+v", len(entries), entries)
	}
	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.Summary.ID] = e
	}
	e, ok := byID["hello"]
	if !ok {
		t.Fatalf("hello is missing from %+v", entries)
	}
	if e.Manifest == nil || e.Manifest.Name != "Hello" ||
		e.Manifest.Kraft == nil || e.Manifest.Kraft.Binary != "bin/srv" {
		t.Fatalf("hello entry = %+v, want the parsed manifest", e)
	}
	if e.Dir != filepath.Join(root, "hello") {
		t.Fatalf("hello entry dir = %q", e.Dir)
	}
	if bad := byID["bad-perm"]; bad.Summary.Error == "" || bad.Manifest != nil {
		t.Fatalf("bad-perm entry = %+v, want an error and no manifest", bad)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != len(entries) {
		t.Fatalf("List returned %d summaries for %d entries", len(list), len(entries))
	}
	for i, sum := range list {
		if !reflect.DeepEqual(sum, entries[i].Summary) {
			t.Fatalf("List[%d] = %+v, want the scan's summary %+v",
				i, sum, entries[i].Summary)
		}
	}
}

// TestManifestIgnoresContributesSegment pins the manifest cleanup: the
// UI half registers from the bundle, so a contributes segment written
// against an older build is accepted and dropped — including duplicate
// panel ids and a pet list, both of which used to reject the manifest
// (pets through the retired pets:contribute gate).
func TestManifestIgnoresContributesSegment(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "legacy-ui", map[string]any{
		"id": "legacy-ui", "name": "Legacy UI", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
		"contributes": map[string]any{
			"settingsPanels": []any{
				map[string]any{"id": "panel", "title": "P", "order": 1},
				map[string]any{"id": "panel", "title": "P again", "order": 2},
			},
			"sidebarEntries": []any{
				map[string]any{"id": "", "title": "no id", "order": 1},
			},
			"pets": []any{map[string]any{"id": "cat"}},
		},
	}, "console.log('legacy')")
	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Error != "" || !list[0].Enabled {
		t.Fatalf("a manifest with a retired contributes segment must "+
			"load: %+v", list)
	}
	if _, err := s.Bundle("legacy-ui"); err != nil {
		t.Fatalf("Bundle: %v", err)
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

func TestStoreAssetReadsBoundedPluginFile(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	dir := filepath.Join(root, "hello", "pets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("rive-bytes")
	if err := os.WriteFile(filepath.Join(dir, "cat.riv"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)
	got, err := s.Asset("hello", "pets/cat.riv")
	if err != nil || string(got) != "rive-bytes" {
		t.Fatalf("Asset = (%q, %v)", got, err)
	}
	if _, err := s.Asset("hello", "../plugin.go"); err == nil {
		t.Fatal("escaping asset path must fail")
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
	if sum.ID != "installed" || !sum.Enabled {
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

func TestInstallMakesKraftExecutable(t *testing.T) {
	srcRoot := t.TempDir()
	writePlugin(t, srcRoot, "cap", map[string]any{
		"id": "cap", "name": "Cap", "version": "1.0.0",
		"entry": "dist/index.js",
		"kraft": map[string]any{"binary": "bin/auth", "protocol": 1},
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
	// for the declared kraft binary.
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("kraft binary is not executable after install: %v", info.Mode())
	}
}

func TestManifestValidatesAgentCapabilities(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "agent", map[string]any{
		"id": "agent", "name": "Agent", "version": "0.1.0",
		"entry": "dist/index.js",
		"kraft": map[string]any{"binary": "bin/agent", "protocol": 1},
		"permissions": []string{
			"skills:provide", "mcp:provide", "hooks:provide", "tools:provide",
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

func TestManifestLegacyCapabilityKey(t *testing.T) {
	root := t.TempDir()
	// The pre-rename spelling: a manifest that still writes the kraft
	// section as "capability" keeps resolving its binary, so plugins
	// installed by older builds survive the rename.
	writePlugin(t, root, "legacy", map[string]any{
		"id": "legacy", "name": "Legacy", "version": "0.1.0",
		"entry":      "dist/index.js",
		"capability": map[string]any{"binary": "bin/old", "protocol": 1},
	}, "")
	s := NewStore(root)
	kraft, ok, err := s.Kraft("legacy")
	if err != nil || !ok || kraft.Binary != "bin/old" {
		t.Fatalf("legacy manifest: Kraft = (%+v, %v, %v)", kraft, ok, err)
	}
}

// TestManifestBothKraftSpellingsSplitByAudience is the section half of
// the rule the charter's legacy table states: a manifest that carries
// both spellings of one section is read by the registry (an installed
// plugin keeps loading, on the new key) and refused by the authoring
// gates, where the author can delete one. Two sections that disagree are
// refused either way — that is a stale value, not a spelling.
func TestManifestBothKraftSpellingsSplitByAudience(t *testing.T) {
	agreed := map[string]any{
		"id": "both", "name": "Both", "version": "0.1.0",
		"entry":      "dist/index.js",
		"kraft":      map[string]any{"binary": "bin/new", "protocol": 1},
		"capability": map[string]any{"binary": "bin/new", "protocol": 1},
	}
	root := t.TempDir()
	writePlugin(t, root, "both", agreed, "bundle")
	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Error != "" {
		t.Fatalf("List = %+v, want the installed plugin to keep loading", list)
	}
	k, ok, err := s.Kraft("both")
	if err != nil || !ok || k.Binary != "bin/new" {
		t.Fatalf("Kraft = (%+v, %v, %v), want the new key's binary", k, ok, err)
	}

	src := t.TempDir()
	writePlugin(t, src, "both", agreed, "bundle")
	if _, err := NewStore(t.TempDir()).Inspect(filepath.Join(src, "both")); err == nil ||
		!strings.Contains(err.Error(), "both kraft") {
		t.Fatalf("Inspect error = %v, want a refusal naming both keys", err)
	}

	disagreed := map[string]any{
		"id": "both", "name": "Both", "version": "0.1.0",
		"entry":      "dist/index.js",
		"kraft":      map[string]any{"binary": "bin/new", "protocol": 1},
		"capability": map[string]any{"binary": "bin/old", "protocol": 1},
	}
	root = t.TempDir()
	writePlugin(t, root, "both", disagreed, "bundle")
	list, err = NewStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || !strings.Contains(list[0].Error, "both kraft") {
		t.Fatalf("List = %+v, want a rejection naming both keys", list)
	}
}

func TestManifestTranslatesLegacyPermissions(t *testing.T) {
	root := t.TempDir()
	// The pre-sweep spellings: a manifest that still writes them keeps
	// loading, and every read path sees the canonical names.
	writePlugin(t, root, "legacy-perms", map[string]any{
		"id": "legacy-perms", "name": "Legacy", "version": "0.1.0",
		"entry": "dist/index.js",
		"kraft": map[string]any{"binary": "bin/x", "protocol": 1},
		"permissions": []string{
			"skills:contribute", "hooks:register", "mcp:contribute", "tools:expose",
		},
		"skills":     []string{"skills"},
		"hooks":      []string{"hooks/hooks.json"},
		"mcpServers": []any{map[string]any{"name": "srv", "transport": "stdio", "command": "bin/srv"}},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "ping",
			"inputSchema": map[string]any{"type": "object"},
		}},
	}, "")
	want := "skills:provide,hooks:provide,mcp:provide,tools:provide"
	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Error != "" {
		t.Fatalf("List = %+v, want a valid plugin", list)
	}
	if got := strings.Join(list[0].Permissions, ","); got != want {
		t.Fatalf("summary permissions = %q, want %q", got, want)
	}
	m, err := s.Manifest("legacy-perms")
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if got := strings.Join(m.Permissions, ","); got != want {
		t.Fatalf("manifest permissions = %q, want %q", got, want)
	}
}

func TestManifestDropsRetiredPermissions(t *testing.T) {
	root := t.TempDir()
	// Retired names are accepted and dropped: rejecting the manifest
	// would kill a plugin's working half over a name that gates
	// nothing.
	writePlugin(t, root, "retired-perms", map[string]any{
		"id": "retired-perms", "name": "Retired", "version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": []string{
			"storage:kv", "commands:register", "statusbar:contribute",
			"events:subscribe",
		},
	}, "")
	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Error != "" {
		t.Fatalf("List = %+v, want a valid plugin", list)
	}
	if got := strings.Join(list[0].Permissions, ","); got != "storage:kv" {
		t.Fatalf("summary permissions = %q, want the retired names gone", got)
	}
}

// TestManifestBothGrantSpellingsSplitByAudience is the grant half: the
// two spellings grant identical authority, so an installed plugin is
// read as the canonical one (and the list collapses to one entry), while
// the authoring gates refuse the redundancy.
func TestManifestBothGrantSpellingsSplitByAudience(t *testing.T) {
	declared := map[string]any{
		"id": "both-perms", "name": "Both", "version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": []string{
			"tools:provide", "tools:expose",
		},
	}
	root := t.TempDir()
	writePlugin(t, root, "both-perms", declared, "bundle")
	list, err := NewStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Error != "" {
		t.Fatalf("List = %+v, want the installed plugin to keep loading", list)
	}
	if got := strings.Join(list[0].Permissions, ","); got != "tools:provide" {
		t.Fatalf("permissions = %q, want the pair collapsed to one grant", got)
	}

	src := t.TempDir()
	writePlugin(t, src, "both-perms", declared, "bundle")
	_, err = NewStore(t.TempDir()).Inspect(filepath.Join(src, "both-perms"))
	if err == nil ||
		!strings.Contains(err.Error(), "tools:expose") ||
		!strings.Contains(err.Error(), "tools:provide") {
		t.Fatalf("Inspect error = %v, want a refusal naming both spellings", err)
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
				"kraft": map[string]any{"binary": "bin/x", "protocol": 1},
				"tools": []any{map[string]any{"name": "t", "method": "m"}},
			},
		},
		{
			name: "tools without kraft",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"tools:provide"},
				"tools":       []any{map[string]any{"name": "t", "method": "m"}},
			},
		},
		{
			name: "mcp unknown transport",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"mcp:provide"},
				"mcpServers":  []any{map[string]any{"name": "s", "transport": "carrier"}},
			},
		},
		{
			name: "skill path escapes",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"skills:provide"},
				"skills":      []string{"../skills"},
			},
		},
		{
			name: "tool schema not object",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"tools:provide"},
				"tools": []any{map[string]any{
					"name": "t", "method": "m", "inputSchema": []string{"not", "object"},
				}},
			},
		},
		{
			name: "mcp server name with separator",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"mcp:provide"},
				"mcpServers":  []any{map[string]any{"name": "bad:name", "transport": "stdio", "command": "bin/s"}},
			},
		},
		{
			name: "tool name with dot",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"kraft":       map[string]any{"binary": "bin/x", "protocol": 1},
				"permissions": []string{"tools:provide"},
				"tools": []any{map[string]any{
					"name": "tool.name", "method": "m",
				}},
			},
		},
		{
			name: "mcp server name with dot",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"permissions": []string{"mcp:provide"},
				"mcpServers":  []any{map[string]any{"name": "bad.name", "transport": "stdio", "command": "bin/s"}},
			},
		},
		{
			name: "tool description too long",
			m: map[string]any{
				"id": "x", "name": "X", "version": "0.1.0", "entry": "dist/index.js",
				"kraft":       map[string]any{"binary": "bin/x", "protocol": 1},
				"permissions": []string{"tools:provide"},
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
				"kraft":       map[string]any{"binary": "bin/x", "protocol": 1},
				"permissions": []string{"tools:provide"},
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
			"skills:provide", "hooks:provide", "mcp:provide",
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
		"permissions": []string{"skills:provide"},
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
	// The registry moved once for the update that landed and not for the
	// one that lost the race.
	if got := s.Revision(); got != 2 {
		t.Fatalf("revision after concurrent updates = %d, want 2", got)
	}
}
