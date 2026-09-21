package core

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestRebuiltHostIsCollectable guards the desktop Runtime's per-Host
// bookkeeping. Two heap profiles of the packaged app, five minutes and
// five rebuilds apart, showed one runtime surviving per rebuild (a
// skills search index plus its event bus, MCP schemas and clients);
// the configure-once marker was a map keyed by *host.Host, and nothing
// ever removed an entry from it, so a retired Host stayed reachable for
// the lifetime of the app. A rebuild has to leave the previous Host
// free to be collected.
func TestRebuiltHostIsCollectable(t *testing.T) {
	probe := rebuildAndRetireHost(t)
	awaitHostCollected(t, probe)
}

// rebuildAndRetireHost assembles a Host, rebuilds it away, and returns a
// weak pointer to the retired one. The strong reference lives only in
// this frame, so no stack slot of the test keeps the Host alive.
func rebuildAndRetireHost(t *testing.T) weak.Pointer[host.Host] {
	t.Helper()
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, t.TempDir(), "")
	c.SetWorkDir(t.TempDir())
	c.Runtime.SetHostConfigurator(func(*host.Host) {})

	ctx := context.Background()
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	retired := c.Runtime.Current()
	if retired == nil {
		t.Fatal("no current host after the first rebuild")
	}
	probe := weak.Make(retired)

	// A settings save or plugin install rebuilds; the previous Host is
	// invalidated and, being idle, closes immediately.
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	if c.Runtime.Current() == retired {
		t.Fatal("the second rebuild reused the host; nothing retired")
	}
	return probe
}

// awaitHostCollected runs GC until the probe reports the Host is gone.
// Teardown finishes on the host's own goroutines, so a few rounds are
// allowed before the object is declared retained.
func awaitHostCollected(t *testing.T, probe weak.Pointer[host.Host]) {
	t.Helper()
	for attempt := 0; attempt < 40; attempt++ {
		runtime.GC()
		if probe.Value() == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Error("the previous host is still reachable after a rebuild: " +
		"the configure-once marker pins the retired runtime")
}
