package plugins

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/GizClaw/opencraft/internal/plugins/runtime"
)

// builtinDir returns the builtin plugin directory next to the test
// executable, mirroring runtime.BuiltinPluginRoot's platform layout.
// Tests build the bundled layout there so the production detection is
// exercised without any environment override.
func builtinDir(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exeDir := filepath.Dir(exe)
	var dir string
	if goruntime.GOOS == "darwin" {
		dir = filepath.Join(exeDir, "..", "Resources", "plugins")
	} else {
		dir = filepath.Join(exeDir, "plugins")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The bundled layout lives next to the shared test executable, so
	// every test that builds it must remove it afterwards — otherwise
	// later tests in the same package would see a leftover builtin
	// plugin.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Clean(dir)
}

func writeBuiltinPlugin(
	t *testing.T,
	dir, id string,
	m map[string]any,
	bundle string,
	bin string,
) {
	t.Helper()
	writePlugin(t, dir, id, m, bundle)
	if bin != "" {
		p := filepath.Join(dir, id, bin)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func testManifest(id, version string) map[string]any {
	return map[string]any{
		"id": id, "name": id, "version": version,
		"entry": "dist/index.js", "permissions": []string{},
		"contributes": map[string]any{},
	}
}

// TestBuiltinListAndBundle verifies an app-bundled plugin (the layout
// next to the executable) shows up as builtin, enabled by default, and
// its entry bundle is readable.
func TestBuiltinListAndBundle(t *testing.T) {
	dir := builtinDir(t)
	writeBuiltinPlugin(t, dir, "demo", testManifest("demo", "0.1.0"),
		"console.log('demo')", "")

	s := NewStore(t.TempDir())
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got *PluginSummary
	for i := range list {
		if list[i].ID == "demo" {
			got = &list[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("builtin plugin missing from List: %+v", list)
	}
	if !got.Builtin || !got.Enabled || got.Version != "0.1.0" {
		t.Fatalf("builtin summary = %+v", got)
	}
	bundle, err := s.Bundle("demo")
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if bundle != "console.log('demo')" {
		t.Fatalf("bundle = %q", bundle)
	}
}

// TestUserPluginShadowsBuiltin verifies a user-installed plugin with
// the same id wins over the builtin.
func TestUserPluginShadowsBuiltin(t *testing.T) {
	dir := builtinDir(t)
	writeBuiltinPlugin(t, dir, "demo", testManifest("demo", "0.1.0"),
		"builtin-bundle", "")
	root := t.TempDir()
	writePlugin(t, root, "demo", testManifest("demo", "0.2.0"),
		"user-bundle")

	s := NewStore(root)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List = %d entries, want 1", len(list))
	}
	if list[0].Builtin || list[0].Version != "0.2.0" {
		t.Fatalf("user plugin must shadow builtin: %+v", list[0])
	}
	bundle, err := s.Bundle("demo")
	if err != nil {
		t.Fatal(err)
	}
	if bundle != "user-bundle" {
		t.Fatalf("bundle = %q, want user-bundle", bundle)
	}
}

// TestBuiltinCannotUninstall verifies builtin plugins can be disabled
// but never removed.
func TestBuiltinCannotUninstall(t *testing.T) {
	dir := builtinDir(t)
	writeBuiltinPlugin(t, dir, "demo", testManifest("demo", "0.1.0"),
		"bundle", "")
	root := t.TempDir()
	s := NewStore(root)

	if err := s.SetEnabled("demo", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Enabled {
		t.Fatalf("builtin should be disabled: %+v", list[0])
	}
	if err := s.Uninstall("demo"); err == nil {
		t.Fatal("Uninstall of a builtin plugin must be rejected")
	}
	if _, err := s.Bundle("demo"); err != nil {
		t.Fatalf("builtin must survive a rejected uninstall: %v", err)
	}
}

// TestBuiltinCapabilityBinaryFallsBack verifies the subprocess runtime
// resolves a builtin plugin's capability binary against the bundled
// root when it is absent from the user root.
func TestBuiltinCapabilityBinaryFallsBack(t *testing.T) {
	dir := builtinDir(t)
	writeBuiltinPlugin(t, dir, "demo", map[string]any{
		"id": "demo", "name": "Demo", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{},
		"capability": map[string]any{
			"binary": "bin/demo-helper", "protocol": 1,
		},
		"contributes": map[string]any{},
	}, "bundle", "bin/demo-helper")

	s := NewStore(t.TempDir())
	cap, ok, err := s.Capability("demo")
	if err != nil || !ok {
		t.Fatalf("Capability = %v, %v", ok, err)
	}
	loader := runtime.DefaultLoader{
		Root:           s.root,
		CapabilityFunc: s.Capability,
	}
	bin, err := loader.BinaryPath("demo", cap)
	if err != nil {
		t.Fatalf("BinaryPath: %v", err)
	}
	want := filepath.Join(dir, "demo", "bin", "demo-helper")
	if bin != want {
		t.Fatalf("binary resolved to %q, want %q", bin, want)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("resolved binary missing: %v", err)
	}
}
