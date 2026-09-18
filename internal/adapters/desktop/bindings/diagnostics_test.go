package bindings

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestReportFrontendErrorEscapesNewlines pins the one-record-per-error
// contract: a React console.error or stack trace carries newlines, and
// an unescaped payload would split the record across physical log lines.
func TestReportFrontendErrorEscapesNewlines(t *testing.T) {
	dir := t.TempDir()
	c := core.NewCore(dir, dir, "")
	c.Shell.SetContext(context.Background())
	capture := logcapture.Install(t)

	NewDiagnosticsBinding(c).ReportFrontendError("console.error",
		"first line\nsecond line",
		"at f (a.ts:1)\r\nat g (b.ts:2)")

	for _, record := range capture.Records() {
		if record.Body().AsString() != "frontend: console.error" {
			continue
		}
		message := logcapture.Attribute(record, "message")
		if strings.ContainsAny(message, "\r\n") {
			t.Fatalf("message must stay on one line: %q", message)
		}
		if message != `first line\nsecond line` {
			t.Fatalf("message = %q, want escaped newline", message)
		}
		stack := logcapture.Attribute(record, "stack")
		if strings.ContainsAny(stack, "\r\n") {
			t.Fatalf("stack must stay on one line: %q", stack)
		}
		if stack != `at f (a.ts:1)\r\nat g (b.ts:2)` {
			t.Fatalf("stack = %q, want escaped CRLF", stack)
		}
		return
	}
	t.Fatal("missing frontend error record")
}

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
