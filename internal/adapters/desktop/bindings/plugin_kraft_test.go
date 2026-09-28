package bindings

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/testing/kraftfixture"
)

// TestPluginMutationsStopTheKraft covers the clock's kraft row at the
// layer that owns it: every mutation the settings page and the desktop
// can drive stops the process the plugin was serving from. Seven Stop
// call sites exist across the two adapters and nothing asserted any of
// them, so deleting one — and the row with it — left the suite green.
// The pid is the observable: the fixture answers with its own, and a
// stopped process is the only reason the next call gets a different one.
func TestPluginMutationsStopTheKraft(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the kraft lifecycle test in short mode")
	}
	binary := kraftfixture.Build(t, kraftfixture.SecretPlugin)
	source := func(t *testing.T, version string) string {
		t.Helper()
		dir := t.TempDir()
		writeFixtureSource(t, dir, binary, version)
		return dir
	}

	// Each subtest owns a registry so one path's leftovers cannot make
	// another's pid comparison pass for the wrong reason, and each
	// comparison brackets only the mutation under test: a pid read
	// before some earlier step would move for that step's stop instead.
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, b *Plugin, source func(*testing.T, string) string)
		mutate func(t *testing.T, b *Plugin, source func(*testing.T, string) string)
	}{
		{
			// SetEnabled stops the process when a plugin is disabled
			// and leaves the next call to start a fresh one.
			name: "disable",
			mutate: func(t *testing.T, b *Plugin, _ func(*testing.T, string) string) {
				if err := b.SetEnabled("plug", false); err != nil {
					t.Fatalf("disable: %v", err)
				}
				if err := b.SetEnabled("plug", true); err != nil {
					t.Fatalf("enable: %v", err)
				}
			},
		},
		{
			name: "update",
			mutate: func(t *testing.T, b *Plugin, source func(*testing.T, string) string) {
				if _, err := b.Update("plug", source(t, "0.2.0")); err != nil {
					t.Fatalf("update: %v", err)
				}
			},
		},
		{
			name: "update zip",
			mutate: func(t *testing.T, b *Plugin, _ func(*testing.T, string) string) {
				zip := filepath.Join(t.TempDir(), "plug.zip")
				zipFixtureSource(t, zip, binary, "0.2.0")
				if _, err := b.UpdateZip("plug", zip); err != nil {
					t.Fatalf("update zip: %v", err)
				}
			},
		},
		{
			// Rollback eats the backup the update left behind, so the
			// update is the setup; the pid it moves belongs to the
			// update's stop, and the one the rollback owns is measured
			// after it.
			name: "rollback",
			setup: func(t *testing.T, b *Plugin, source func(*testing.T, string) string) {
				if _, err := b.Update("plug", source(t, "0.2.0")); err != nil {
					t.Fatalf("update: %v", err)
				}
			},
			mutate: func(t *testing.T, b *Plugin, _ func(*testing.T, string) string) {
				if _, err := b.Rollback("plug"); err != nil {
					t.Fatalf("rollback: %v", err)
				}
			},
		},
		{
			name: "uninstall",
			mutate: func(t *testing.T, b *Plugin, source func(*testing.T, string) string) {
				if err := b.Uninstall("plug"); err != nil {
					t.Fatalf("uninstall: %v", err)
				}
				// Reinstalling the same id is what makes the stop
				// observable here: a process that survived the
				// uninstall would answer the next call with its old
				// pid.
				if _, err := b.Install(source(t, "0.1.0")); err != nil {
					t.Fatalf("reinstall: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newKraftBinding(t, binary)
			if tc.setup != nil {
				tc.setup(t, b, source)
			}
			before := invokeFixturePID(t, b)
			tc.mutate(t, b, source)
			if after := invokeFixturePID(t, b); after == before {
				t.Fatalf("kraft pid after %s = %d, unchanged: the mutation "+
					"left the process it replaced running", tc.name, after)
			}
		})
	}
}

// newKraftBinding installs a fixture plugin through the binding and
// returns the binding wired to it.
func newKraftBinding(t *testing.T, binary string) *Plugin {
	t.Helper()
	dir := t.TempDir()
	c := core.NewCore(dir, dir, "")
	t.Cleanup(c.Plugin.Close)
	c.Plugin.Kraft.SetTimeouts(30*time.Second, 60*time.Second)
	b := NewPluginBinding(c)
	src := t.TempDir()
	writeFixtureSource(t, src, binary, "0.1.0")
	if _, err := b.Install(src); err != nil {
		t.Fatalf("install: %v", err)
	}
	return b
}

// invokeFixturePID starts the plugin's kraft if it is not running and
// returns the pid it answers with.
func invokeFixturePID(t *testing.T, b *Plugin) int {
	t.Helper()
	raw, err := b.Invoke("plug", kraftfixture.PIDMethod, "")
	if err != nil {
		t.Fatalf("invoke %s: %v", kraftfixture.PIDMethod, err)
	}
	var out struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s answer %s: %v", kraftfixture.PIDMethod, raw, err)
	}
	if out.PID <= 0 {
		t.Fatalf("fixture reported pid %d", out.PID)
	}
	return out.PID
}

// writeFixtureSource writes a plugin tree whose kraft is the fixture
// binary, under bin/kraft as the manifest says.
func writeFixtureSource(t *testing.T, dir, binary, version string) {
	t.Helper()
	files := map[string]string{
		"plugin.json": `{
			"id": "plug", "name": "Plug", "version": "` + version + `",
			"entry": "dist/index.js",
			"permissions": ["tools:provide"],
			"tools": [{"name": "pid", "description": "Report the kraft pid",
				"method": "` + kraftfixture.PIDMethod + `",
				"inputSchema": {"type": "object"}}],
			"kraft": {"binary": "bin/kraft", "protocol": 1}
		}`,
		"dist/index.js": "export const apply = () => {};",
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	copyTestExecutable(t, binary, filepath.Join(dir, "bin", "kraft"))
}

// zipFixtureSource packs the same tree as a plugin package.
func zipFixtureSource(t *testing.T, path, binary, version string) {
	t.Helper()
	dir := t.TempDir()
	writeFixtureSource(t, dir, binary, version)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		// The binary keeps its executable bit through the archive: the
		// registry restores 0700 for a declared kraft binary.
		_, err = io.Copy(w, src)
		return err
	})
	if err != nil {
		t.Fatalf("pack plugin: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

// copyTestExecutable writes a copy of one executable, creating the
// parent directory.
func copyTestExecutable(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture binary: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o700); err != nil {
		t.Fatalf("write fixture binary: %v", err)
	}
}
