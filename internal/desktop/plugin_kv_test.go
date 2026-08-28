package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginKVSetGetListDelete(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"storage:kv"},
	}, "")
	a := pluginApp(t, root)

	got, err := a.PluginKVGet("hello", "counter")
	if err != nil {
		t.Fatalf("PluginKVGet: %v", err)
	}
	if got.Value != "" {
		t.Fatalf("unexpected initial value %q", got.Value)
	}
	if err := a.PluginKVSet("hello", "counter", "3"); err != nil {
		t.Fatalf("PluginKVSet: %v", err)
	}
	if err := a.PluginKVSet("hello", "label", "hello world"); err != nil {
		t.Fatalf("PluginKVSet: %v", err)
	}
	got, _ = a.PluginKVGet("hello", "counter")
	if got.Value != "3" {
		t.Fatalf("counter = %q, want 3", got.Value)
	}
	list, err := a.PluginKVList("hello")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if err := a.PluginKVDelete("hello", "counter"); err != nil {
		t.Fatalf("PluginKVDelete: %v", err)
	}
	got, _ = a.PluginKVGet("hello", "counter")
	if got.Value != "" {
		t.Fatalf("counter still present after delete")
	}
}

func TestPluginKVPersistsAcrossReopen(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"storage:kv"},
	}, "")
	a := pluginApp(t, root)
	if err := a.PluginKVSet("hello", "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	again := pluginApp(t, root)
	got, err := again.PluginKVGet("hello", "theme")
	if err != nil || got.Value != "dark" {
		t.Fatalf("reopened kv = (%+v, %v), want dark", got, err)
	}
}

func TestPluginKVValidationAndUninstallCleanup(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"storage:kv"},
	}, "")
	a := pluginApp(t, root)
	if err := a.PluginKVSet("hello", "../evil", "x"); err == nil {
		t.Fatal("invalid key should fail")
	}
	if err := a.PluginKVSet("hello", "big", string(make([]byte, kvMaxValueBytes+1))); err == nil {
		t.Fatal("oversized value should fail")
	}
	if err := a.PluginKVSet("not-installed", "k", "v"); err == nil {
		t.Fatal("kv for a non-installed plugin should fail")
	}
	if err := a.PluginKVSet("hello", "k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := a.PluginUninstall("hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".data", "hello")); !os.IsNotExist(err) {
		t.Fatalf("kv data should be removed on uninstall, stat err = %v", err)
	}
}
