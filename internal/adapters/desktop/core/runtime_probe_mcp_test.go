package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/httpprobe"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestReloadReconcilesProbeWithMCPConfiguration pins the guard that keeps
// the diagnostic round-trip probe (the DEV switch or OPENCRAFT_HTTP_PROBE,
// see foundation/utils/httpprobe) from breaking an HTTP MCP server.
//
// A streamable-HTTP MCP client is built inside a reload — flowcraft
// deploys the mcp tool through core/utils.NewRoundTripper, which clones
// http.DefaultTransport through an unchecked *http.Transport assertion —
// and a wrapped process transport makes that assertion panic. The probe is
// a diagnostic, so it stands down while such a server is configured and
// comes back when the last one is removed, in both cases before the reload
// builds clients from the document. The switch itself stays on: the card
// reports the stand-down rather than dropping the user's choice.
func TestReloadReconcilesProbeWithMCPConfiguration(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())
	// The switch is what is under test here, not the env override: clear
	// it so a developer's shell cannot flip the result.
	t.Setenv(httpprobe.Env, "")

	c := NewCore(configDir, t.TempDir(), "")
	ctx := context.Background()
	c.SetWorkDir(workDir)
	t.Cleanup(func() { httpprobe.Uninstall() })

	// With no MCP server in the way, flipping the switch installs the
	// probe before the assembly the reload runs.
	if err := c.SetHTTPProbe(ctx, true); err != nil {
		t.Fatalf("enable probe: %v", err)
	}
	if !httpprobe.Active() {
		t.Fatal("the switch did not install the probe")
	}

	recorder := logcapture.Install(t)
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		}))
	defer server.Close()
	if err := config.WriteMCP(configDir, []config.MCPServer{{
		Name:      "remote",
		Transport: "http",
		URL:       server.URL,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild with an HTTP MCP server: %v", err)
	}
	if httpprobe.Active() {
		t.Fatal("probe is still installed with an HTTP MCP server configured")
	}
	if !slices.Contains(recorder.Bodies(), "httpprobe: probe stood down") {
		t.Fatalf("no stand-down record, bodies: %v", recorder.Bodies())
	}
	state := c.HTTPProbeState()
	if !state.Enabled || state.Active || state.Blocker == "" {
		t.Fatalf("probe state = %+v, want enabled but parked by the blocker", state)
	}

	// Clearing the server brings the probe back on the next reload, which
	// then assembles the provider clients against the wrapped transport.
	if err := config.WriteMCP(configDir, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild after clearing MCP: %v", err)
	}
	if !httpprobe.Active() {
		t.Fatal("probe did not return after the HTTP MCP server was removed")
	}
}
