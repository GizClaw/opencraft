package core

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPluginSecretE2E drives the path a real plugin kraft takes to the
// keyring: subprocess handshake → secret.set/secret.get primitives over
// JSON-RPC → the host's secrets:auth gate → the credential store. It
// covers the wiring the vocabulary sweep added (the store-backed
// permission lookup), not just the gate's unit behavior.
func TestPluginSecretE2E(t *testing.T) {
	if testing.Short() {
		// This test compiles, copies and repeatedly spawns a kraft
		// fixture binary; `go test -short ./...` skips the subprocess
		// plumbing and keeps the fast local loop fast.
		t.Skip("skipping plugin subprocess end-to-end test in short mode")
	}
	binary := buildSecretPlugin(t)
	warmPlugin(t, binary)

	t.Run("granted", func(t *testing.T) {
		c, _ := newFixturePluginCore(t, binary, "Secrets Plugin",
			[]string{"secrets:auth"})
		result := probeSecret(t, c)
		if result["stored"] != true || result["value"] != "probe-secret" {
			t.Fatalf("plugin reported %+v, want the token stored and read back", result)
		}
		if reason, _ := result["error"].(string); reason != "" {
			t.Fatalf("plugin-visible error = %q", reason)
		}
		// The host-side store holds it under the plugin namespace.
		got, found, err := c.Plugin.Secrets.Get(
			context.Background(), "auth/plug/probe-token")
		if err != nil || !found || got != "probe-secret" {
			t.Fatalf("keyring = (%q, %v, %v)", got, found, err)
		}
	})

	t.Run("denied without permission", func(t *testing.T) {
		c, _ := newFixturePluginCore(t, binary, "Secrets Plugin",
			[]string{"storage:kv"})
		result := probeSecret(t, c)
		if result["stored"] == true {
			t.Fatalf("plugin reported %+v, want the call refused", result)
		}
		reason, _ := result["error"].(string)
		if !strings.Contains(reason, "secrets:auth") {
			t.Fatalf("plugin-visible error = %q", reason)
		}
		if _, found, err := c.Plugin.Secrets.Get(
			context.Background(), "auth/plug/probe-token"); err != nil || found {
			t.Fatalf("keyring found = %v, %v; want nothing stored", found, err)
		}
	})
}

func probeSecret(t *testing.T, c *Core) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Starting a freshly written binary can exceed the handshake window.
	raw, err := c.Plugin.Kraft.Invoke(ctx, "plug", "secret.probe", nil)
	if err != nil {
		t.Fatalf("invoke secret.probe: %v", err)
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode plugin result %s: %v", raw, err)
	}
	return result
}

// buildSecretPlugin compiles the kraft fixture once per test binary run.
func buildSecretPlugin(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "secretplugin")
	build := exec.Command("go", "build", "-o", binary, "./testdata/secretplugin")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build secret plugin: %v\n%s", err, out)
	}
	return binary
}
