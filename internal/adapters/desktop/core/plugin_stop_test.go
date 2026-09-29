package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/kraftfixture"
)

// fixturePluginSource writes a plugin tree whose kraft is the fixture
// binary, so a test can start the subprocess and read its pid. The one
// declared tool is the fixture's own pid method, which is what a turn
// that installs this plugin mid-run calls.
func fixturePluginSource(t *testing.T, dir, binary, id, version string) {
	t.Helper()
	manifest := fmt.Sprintf(
		`{"id": %q, "name": %q, "version": %q, `+
			`"entry": "dist/index.js", "permissions": ["tools:provide"], `+
			`"tools": [{"name": "pid", "description": "Report the kraft pid", `+
			`"method": %q, "inputSchema": {"type": "object"}}], `+
			`"kraft": {"binary": "bin/kraft", "protocol": 1}}`,
		id, id+" fixture", version, kraftfixture.PIDMethod)
	files := map[string]string{
		"plugin.json":   manifest,
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
	copyExecutable(t, binary, filepath.Join(dir, "bin", "kraft"))
}

// fixturePID starts the plugin's kraft if it is not running and returns
// the pid it answers with. A mutation that stops the process makes the
// next call start a new one, so the pid is the observable the clock's
// kraft row needs: the process itself leaves no other trace.
func fixturePID(t *testing.T, c *Core, id string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := c.Plugin.Kraft.Invoke(ctx, id, kraftfixture.PIDMethod, nil)
	if err != nil {
		t.Fatalf("invoke %s on %s: %v", kraftfixture.PIDMethod, id, err)
	}
	var out struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s answer %s: %v", kraftfixture.PIDMethod, raw, err)
	}
	if out.PID <= 0 {
		t.Fatalf("fixture reported pid %d", out.PID)
	}
	return out.PID
}

// TestPluginUpdateStopsTheKraft was the untested half of the clock's
// kraft row (capabilities/plugins' charter, FaceRefreshes): an update
// swaps the plugin directory and then stops the process the previous
// version was serving from. Nothing asserted any of the Stop call sites,
// so deleting this one — and the row with it — left the suite green. The
// pid is the assertion: the plugin answers with its own, and only a
// stopped process gives the next call a different one.
func TestPluginUpdateStopsTheKraft(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the kraft lifecycle test in short mode")
	}
	c, workDir, _ := newInstallerTestCore(t)
	// A freshly built binary can exceed the default handshake window on
	// a loaded machine.
	c.Plugin.Kraft.SetTimeouts(30*time.Second, 60*time.Second)
	binary := kraftfixture.Build(t, kraftfixture.SecretPlugin)

	src := filepath.Join(workDir, ".opencraft-plugins", "plug")
	fixturePluginSource(t, src, binary, "plug", "0.1.0")
	if _, err := c.Plugin.Store.Install(src); err != nil {
		t.Fatalf("install: %v", err)
	}
	before := fixturePID(t, c, "plug")

	next := t.TempDir()
	fixturePluginSource(t, next, binary, "plug", "0.2.0")
	if _, err := NewPluginInstaller(c).PluginUpdate(
		context.Background(), "plug", next,
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	if after := fixturePID(t, c, "plug"); after == before {
		t.Fatalf("kraft pid after the update = %d, unchanged: the update "+
			"left the process it replaced running", after)
	}
}
