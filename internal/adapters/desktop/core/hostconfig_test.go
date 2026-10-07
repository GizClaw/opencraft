package core

import (
	"os"
	"testing"

	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestHostSpeaksOnlyForTheWindowScope pins the workspace half of the
// observer gate: events a Host reports about its own turns reach the
// window only while that Host is the one serving the window's
// workspace. A Host the window has left (or one a rebuild already
// retired) keeps running — a staged draft drains there — and its
// artifacts and session updates must not land in the transcript the
// window is showing.
func TestHostSpeaksOnlyForTheWindowScope(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, t.TempDir(), "")
	c.SetWorkDir(t.TempDir())
	c.Runtime.Manager().SetHostConfigurator(c.ConfigureHost)
	if err := c.RebuildRuntime(c.Shell.Context()); err != nil {
		t.Fatalf("rebuild runtime: %v", err)
	}
	serving := c.ActiveHost()
	if serving == nil {
		t.Fatal("no host after the rebuild")
	}
	if !c.hostSpeaks(serving) {
		t.Fatal("the host serving the window's workspace does not speak")
	}

	// A second rebuild retires it: same workspace, new generation. The
	// retired Host is still alive for whatever it was running, but the
	// window's transcript belongs to its replacement.
	if err := c.RebuildRuntime(c.Shell.Context()); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	replacement := c.ActiveHost()
	if replacement == nil || replacement == serving {
		t.Fatalf("second rebuild did not replace the host: %v", replacement)
	}
	if c.hostSpeaks(serving) {
		t.Error("the retired host still speaks for the window")
	}
	if !c.hostSpeaks(replacement) {
		t.Error("the replacement host does not speak")
	}

	// An open window with no workspace has no current Host at all, so
	// nothing a stale Host reports can reach it.
	c.SetWorkDir("")
	if c.hostSpeaks(replacement) {
		t.Error("a host speaks for a window that has left every workspace")
	}
	if c.hostSpeaks(nil) {
		t.Error("a nil host speaks")
	}
}
