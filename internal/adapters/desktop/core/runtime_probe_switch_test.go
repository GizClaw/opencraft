package core

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/utils/httpprobe"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestProbeFollowsTheDiagnosticsSwitch pins the two opt-ins, the default
// they share and the promise the card makes: a fresh profile with no env
// var leaves the process transport alone, the DEV switch installs the
// wrapper and the reload that follows rebuilds the provider clients
// against it (a turn then leaves "request dispatched" records), switching
// off quiets even the runtime that was built while the probe was on, and
// OPENCRAFT_HTTP_PROBE still forces it on over an off switch, so a
// support session can ask for records without editing desktop.json.
func TestProbeFollowsTheDiagnosticsSwitch(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())
	t.Setenv(httpprobe.Env, "")

	recorder := logcapture.Install(t)
	dispatches := func() int {
		count := 0
		for _, record := range recorder.Records() {
			if record.Body().AsString() == "httpprobe: request dispatched" {
				count++
			}
		}
		return count
	}

	c := NewCore(configDir, t.TempDir(), "")
	ctx := context.Background()
	c.SetWorkDir(workDir)
	t.Cleanup(func() { httpprobe.Uninstall() })

	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	if httpprobe.Active() {
		t.Fatal("the probe installed itself without the env var or the switch")
	}
	runProbeTurn(t, c)
	if got := dispatches(); got != 0 {
		t.Fatalf("probe records with the switch off = %d, want 0", got)
	}
	base := http.DefaultTransport

	// The switch installs the wrapper and reloads; the turn that follows
	// runs on clients built against it.
	if err := c.SetHTTPProbe(ctx, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !httpprobe.Active() {
		t.Fatal("the switch did not install the probe")
	}
	runProbeTurn(t, c)
	if got := dispatches(); got == 0 {
		t.Fatal("no probe records after the switch was turned on")
	}

	// Switching off needs no reload: the wrapper reads the installed
	// flag per round trip, so the runtime built while the probe was on
	// goes quiet where it stands.
	before := dispatches()
	if err := c.SetHTTPProbe(ctx, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if httpprobe.Active() {
		t.Fatal("the switch off left the probe installed")
	}
	if http.DefaultTransport != base {
		t.Fatal("the switch off did not restore the process transport")
	}
	runProbeTurn(t, c)
	if got := dispatches(); got != before {
		t.Fatalf("probe records after switching off = %d, want %d", got, before)
	}

	// The env var overrides the persisted switch, which is what makes it
	// usable before the app can even show the card.
	t.Setenv(httpprobe.Env, "1")
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild under the env override: %v", err)
	}
	if !httpprobe.Active() {
		t.Fatal("OPENCRAFT_HTTP_PROBE did not force the probe on")
	}
}

// runProbeTurn drives one completed turn through the current Host, which
// is what makes the probe observable: the provider request goes out while
// the recorder is listening.
func runProbeTurn(t *testing.T, c *Core) {
	t.Helper()
	h := c.ActiveHost()
	if h == nil {
		t.Fatal("no current host")
	}
	run, err := h.StartRun(context.Background(), host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "probe check"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if res, err := run.Wait(context.Background()); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("run wait = %v, %v; want completed", res, err)
	}
}
