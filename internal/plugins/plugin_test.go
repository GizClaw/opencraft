package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
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
