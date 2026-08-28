package desktop

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

func pluginApp(t *testing.T, root string) *App {
	t.Helper()
	return &App{pluginDir: root}
}

func TestPluginListScansAndValidates(t *testing.T) {
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
	// A non-plugin directory (no plugin.json) is skipped silently.
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}

	a := pluginApp(t, root)
	list, err := a.PluginList()
	if err != nil {
		t.Fatalf("PluginList: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("PluginList returned %d plugins, want 3: %+v", len(list), list)
	}
	byID := map[string]PluginSummary{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if h := byID["hello"]; !h.Enabled || h.Error != "" || len(h.Panels) != 1 || h.Panels[0] != "hello-panel" {
		t.Fatalf("hello plugin summary = %+v", h)
	}
	if b := byID["bad-perm"]; b.Error == "" {
		t.Fatal("bad-perm should be rejected (unknown permission)")
	}
	if b := byID["bad-id"]; b.Error == "" {
		t.Fatal("bad-id should be rejected (manifest id mismatch)")
	}
}

func TestPluginBundleValidatesPath(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "console.log('hello')")
	a := pluginApp(t, root)
	src, err := a.PluginBundle("hello")
	if err != nil {
		t.Fatalf("PluginBundle: %v", err)
	}
	if src != "console.log('hello')" {
		t.Fatalf("bundle = %q", src)
	}

	// Traversal must be rejected.
	writePlugin(t, root, "evil", map[string]any{
		"id": "evil", "name": "Evil", "version": "0.1.0",
		"entry": "../outside.js", "permissions": []string{},
	}, "")
	if _, err := a.PluginBundle("evil"); err == nil {
		t.Fatal("PluginBundle with escaping entry should fail")
	}
	if _, err := a.PluginBundle("../hello"); err == nil {
		t.Fatal("PluginBundle with invalid id should fail")
	}
}

func TestPluginSetEnabledTogglesState(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "")
	a := pluginApp(t, root)
	if err := a.PluginSetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	list, err := a.PluginList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("plugin should be disabled: %+v", list)
	}
	if err := a.PluginSetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	list, _ = a.PluginList()
	if !list[0].Enabled {
		t.Fatal("plugin should be enabled again")
	}
	if err := a.PluginSetEnabled("missing", true); err == nil {
		t.Fatal("enabling a non-installed plugin should fail")
	}
}
