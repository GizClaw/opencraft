package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
)

// TestPluginTelemetryE2E drives the whole path a real capability plugin
// takes: subprocess handshake → telemetry.configure primitive over
// JSON-RPC → host permission check → pipeline swap → audit trail.
func TestPluginTelemetryE2E(t *testing.T) {
	binary := buildTelemetryPlugin(t)
	warmTelemetryPlugin(t, binary)

	t.Run("granted", func(t *testing.T) {
		c, dataDir := newTelemetryPluginCore(t, binary,
			[]string{"telemetry:export"})
		result := probeTelemetry(t, c, map[string]any{
			"endpoint": "collector.example:4318",
			"headers":  map[string]string{"authorization": "Bearer plugin"},
		})
		if configured, _ := result["configured"].(bool); !configured {
			t.Fatalf("plugin reported %+v, want the sink installed", result)
		}
		sink, owner := c.Telemetry.Sink()
		if owner != "plug" || sink.Endpoint != "collector.example:4318" {
			t.Fatalf("sink = %+v owner %q", sink, owner)
		}
		if got := sink.Headers["Authorization"]; got != "Bearer plugin" {
			t.Fatalf("header = %q", got)
		}
		entries := readTelemetryAudit(t, dataDir)
		if len(entries) != 1 || entries[0].Action != octelemetry.AuditInstall ||
			len(entries[0].HeaderNames) != 1 {
			t.Fatalf("audit entries = %+v", entries)
		}
	})

	t.Run("denied without permission", func(t *testing.T) {
		c, dataDir := newTelemetryPluginCore(t, binary, []string{"storage:kv"})
		result := probeTelemetry(t, c, map[string]any{
			"endpoint": "collector.example:4318",
		})
		if configured, _ := result["configured"].(bool); configured {
			t.Fatalf("plugin reported %+v, want the call refused", result)
		}
		reason, _ := result["error"].(string)
		if !strings.Contains(reason, "lacks telemetry:export") {
			t.Fatalf("plugin-visible error = %q", reason)
		}
		if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
			t.Fatalf("sink = %+v owner %q, want none installed", sink, owner)
		}
		entries := readTelemetryAudit(t, dataDir)
		if len(entries) != 1 || entries[0].Action != octelemetry.AuditDeny {
			t.Fatalf("audit entries = %+v", entries)
		}
	})

	// A plugin that dies on its own loses its sink: the host cannot ask
	// it anything anymore, so it must not keep exporting on its behalf.
	t.Run("crash drops the sink", func(t *testing.T) {
		c, _ := newTelemetryPluginCore(t, binary, []string{"telemetry:export"})
		if result := probeTelemetry(t, c, map[string]any{
			"endpoint": "collector.example:4318",
			"exit":     true,
		}); result["configured"] != true {
			t.Fatalf("plugin reported %+v, want the sink installed first", result)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if sink, owner := c.Telemetry.Sink(); sink.Endpoint == "" && owner == "" {
				return
			}
			if time.Now().After(deadline) {
				sink, owner := c.Telemetry.Sink()
				t.Fatalf("sink %+v owner %q survived the plugin process exit",
					sink, owner)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})

	// The user switch suspends and restores the sink without the plugin
	// re-running its configure call.
	t.Run("switch restores the sink", func(t *testing.T) {
		c, _ := newTelemetryPluginCore(t, binary, []string{"telemetry:export"})
		if result := probeTelemetry(t, c, map[string]any{
			"endpoint": "collector.example:4318",
			"headers":  map[string]string{"authorization": "Bearer plugin"},
		}); result["configured"] != true {
			t.Fatalf("plugin reported %+v, want the sink installed", result)
		}
		if err := c.SetPluginTelemetryExport(false); err != nil {
			t.Fatalf("switch off: %v", err)
		}
		if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
			t.Fatalf("sink = %+v owner %q still active after the switch went off",
				sink, owner)
		}
		if err := c.SetPluginTelemetryExport(true); err != nil {
			t.Fatalf("switch on: %v", err)
		}
		sink, owner := c.Telemetry.Sink()
		if owner != "plug" || sink.Endpoint != "collector.example:4318" {
			t.Fatalf("sink = %+v owner %q, want the restored plugin sink",
				sink, owner)
		}
		if got := sink.Headers["Authorization"]; got != "Bearer plugin" {
			t.Fatalf("restored header = %q", got)
		}
	})
}

// probeTelemetry calls the fixture plugin and returns what it reported
// about the host's answer to its own telemetry.configure primitive.
func probeTelemetry(t *testing.T, c *Core, params map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Starting a freshly written binary can exceed the handshake window
	raw, err := c.Plugin.Capability.Invoke(ctx, "plug", "telemetry.probe", params)
	if err != nil {
		t.Fatalf("invoke telemetry.probe: %v", err)
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode plugin result %s: %v", raw, err)
	}
	return result
}

// warmTelemetryPlugin runs the fixture once with stdin closed: it
// announces itself and exits on EOF. The exec validates the code
// signature and warms the page cache, so the subtests below do not pay
// that cost inside their handshake window.
func warmTelemetryPlugin(t *testing.T, binary string) {
	t.Helper()
	cmd := exec.Command(binary)
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("warm telemetry plugin: %v\n%s", err, out)
	}
}

// newTelemetryPluginCore installs the fixture plugin into a fresh data
// dir and wires the desktop core to it.
func newTelemetryPluginCore(
	t *testing.T,
	binary string,
	permissions []string,
) (*Core, string) {
	t.Helper()
	dataDir := t.TempDir()
	pluginDir := filepath.Join(dataDir, "plugins", "plug")
	if err := os.MkdirAll(filepath.Join(pluginDir, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	perms, err := json.Marshal(permissions)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "plug",
		"name": "Telemetry Plugin",
		"version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": ` + string(perms) + `,
		"capability": {"binary": "helper", "protocol": 1}
	}`
	if err := os.WriteFile(
		filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "dist", "index.js"),
		[]byte("export const apply = () => {};"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, binary, filepath.Join(pluginDir, "helper"))

	c := NewCore(t.TempDir(), dataDir, "")
	c.Telemetry = newTestPipeline(t)
	// `go test ./...` oversubscribes the machine, and the default 5s
	// handshake window turns that scheduling noise into a plugin
	// failure. The fixtures are healthy; give them room.
	c.Plugin.Capability.SetTimeouts(30*time.Second, 60*time.Second)
	t.Cleanup(c.Plugin.Close)
	return c, dataDir
}

func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read plugin binary: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o700); err != nil {
		t.Fatalf("write plugin binary: %v", err)
	}
}

// buildTelemetryPlugin compiles the capability-plugin fixture once per
// test binary run.
func buildTelemetryPlugin(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "telemetryplugin")
	build := exec.Command("go", "build", "-o", binary, "./testdata/telemetryplugin")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build telemetry plugin: %v\n%s", err, out)
	}
	return binary
}
