package bindings

import (
	"context"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
)

func TestDiagnosticsReportSandboxBackend(t *testing.T) {
	dir := t.TempDir()
	c := core.NewCore(dir, dir, "")
	c.Shell.SetContext(context.Background())
	rep := NewDiagnosticsBinding(c).Diagnostics()
	if rep.SandboxBackend == "" {
		t.Fatal("sandbox_backend must not be empty")
	}
	switch rep.SandboxBackend {
	case "seatbelt":
		if !rep.SandboxAvailable {
			t.Fatal("seatbelt is the configured mac backend and must be available")
		}
	case "bwrap":
		// Availability depends on whether bwrap is installed; the
		// field itself is what the UI needs to render a verdict.
	default:
		if !rep.SandboxAvailable {
			t.Fatal("local fallback sandbox must be available")
		}
	}
}
