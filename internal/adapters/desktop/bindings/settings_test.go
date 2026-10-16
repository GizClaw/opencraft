package bindings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
)

func firstRunMode() string {
	if profile.YoloOnly() {
		return "yolo"
	}
	return "workspace"
}

// TestReadLogCapsTheTail pins the ceiling on a requested tail: the read
// window grows with n (64 KiB per line), so an unbounded count would
// pull the whole multi-megabyte log into memory.
func TestReadLogCapsTheTail(t *testing.T) {
	dataDir := t.TempDir()
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const lines = maxLogLines + 500
	var sb strings.Builder
	for i := range lines {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(
		filepath.Join(logDir, "opencraft.log"), []byte(sb.String()), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	b := NewSettingsBinding(core.NewCore(t.TempDir(), dataDir, ""))
	got, err := b.ReadLog(1_000_000)
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	tail := strings.Split(got, "\n")
	if len(tail) != maxLogLines {
		t.Fatalf("ReadLog returned %d lines, want the cap %d",
			len(tail), maxLogLines)
	}
	if want := fmt.Sprintf("line %d", lines-1); tail[len(tail)-1] != want {
		t.Errorf("newest line = %q, want %q", tail[len(tail)-1], want)
	}
	if want := fmt.Sprintf("line %d", lines-maxLogLines); tail[0] != want {
		t.Errorf("oldest line = %q, want %q", tail[0], want)
	}
}

func TestSettingsSessionDefaultsRoundTrip(t *testing.T) {
	userDir := t.TempDir()
	b := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))

	got := b.GetSessionDefaults()
	if got.Mode != firstRunMode() || got.Think != "medium" {
		t.Fatalf("first-run defaults = (%q, %q)", got.Mode, got.Think)
	}
	persistMode := "read-only"
	if profile.YoloOnly() {
		persistMode = "yolo"
	}
	if err := b.SetSessionDefaults(SessionDefaults{
		Mode: persistMode, Think: "high",
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))
	got = reloaded.GetSessionDefaults()
	if got.Mode != persistMode || got.Think != "high" {
		t.Fatalf("defaults after reload = (%q, %q)", got.Mode, got.Think)
	}
}

// TestSetThinkModelWithoutRunningWorkspaceIsRejected pins the write
// discipline: with no Host there is nothing to persist through, and a
// silent success would leave the in-memory knob claiming a value the
// store never accepted.
func TestSetThinkModelWithoutRunningWorkspaceIsRejected(t *testing.T) {
	userDir := t.TempDir()
	b := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))

	before, err := b.GetThink()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetThink("high"); err == nil {
		t.Fatal("SetThink succeeded without a running workspace")
	}
	after, err := b.GetThink()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("in-memory think changed to %q despite the failed write", after)
	}
	if err := b.SetModel("gpt-x"); err == nil {
		t.Fatal("SetModel succeeded without a running workspace")
	}
}

func TestSettingsSessionDefaultsRejectsInvalidValues(t *testing.T) {
	userDir := t.TempDir()
	b := NewSettingsBinding(core.NewCore(userDir, t.TempDir(), ""))

	if err := b.SetSessionDefaults(SessionDefaults{
		Mode: "unrestricted", Think: "medium",
	}); err == nil {
		t.Fatal("unknown mode was accepted")
	}
	validMode := "workspace"
	if profile.YoloOnly() {
		validMode = "yolo"
	}
	if err := b.SetSessionDefaults(SessionDefaults{
		Mode: validMode, Think: "ultra",
	}); err == nil {
		t.Fatal("unknown think level was accepted")
	}

	got := b.GetSessionDefaults()
	if got.Mode != firstRunMode() || got.Think != "medium" {
		t.Fatalf(
			"rejected write changed defaults to (%q, %q)",
			got.Mode, got.Think,
		)
	}
}
