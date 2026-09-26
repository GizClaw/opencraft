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
	if rep.SandboxAvailableReason == "" {
		t.Fatal("sandbox_available_reason must explain the verdict")
	}
	switch rep.SandboxBackend {
	case "seatbelt":
		if !rep.SandboxAvailable {
			t.Fatal("seatbelt is the configured mac backend and must be available")
		}
	case "bwrap":
		// Availability depends on whether bwrap is installed; the
		// field itself is what the UI needs to render a verdict.
	case "jobobject":
		if !rep.SandboxAvailable {
			t.Fatal("the windows job-object backend is built in")
		}
	default:
		if !rep.SandboxAvailable {
			t.Fatal("local fallback sandbox must be available")
		}
	}
}

// TestSandboxBackendFor covers every platform branch on any host: the
// verdict the diagnostics page shows must follow the sandbox
// capability, not a second copy of the platform switch (the Windows
// row used to be reported as "local", and the reason field is what
// distinguishes "nothing to install" from "missing binary").
func TestSandboxBackendFor(t *testing.T) {
	cases := []struct {
		goos       string
		probeFound bool
		name       string
		available  bool
		reasonPart string
	}{
		{"darwin", true, "seatbelt", true, "sandbox-exec is on PATH"},
		{"darwin", false, "seatbelt", false, "sandbox-exec is not on PATH"},
		{"linux", true, "bwrap", true, "bwrap is on PATH"},
		{"linux", false, "bwrap", false, "bwrap is not on PATH"},
		{"windows", false, "jobobject", true, "built into the OS backend"},
		{"freebsd", false, "local", true, "no OS sandbox"},
	}
	for _, tc := range cases {
		name, available, reason := sandboxBackendFor(tc.goos, tc.probeFound)
		if name != tc.name {
			t.Errorf("%s/%v: name = %q, want %q",
				tc.goos, tc.probeFound, name, tc.name)
		}
		if available != tc.available {
			t.Errorf("%s/%v: available = %v, want %v",
				tc.goos, tc.probeFound, available, tc.available)
		}
		if !strings.Contains(reason, tc.reasonPart) {
			t.Errorf("%s/%v: reason = %q, want it to mention %q",
				tc.goos, tc.probeFound, reason, tc.reasonPart)
		}
	}
}

// TestReportFrontendPerfSanitizesLabels pins the renderer trust boundary:
// only whitelisted label keys land in user.db, values are cleaned and
// capped, the unit attribute stays the one the sample declared through
// Unit, and one report writes one shared timestamp.
func TestReportFrontendPerfSanitizesLabels(t *testing.T) {
	dir := t.TempDir()
	c := core.NewCore(dir, dir, "")
	c.Shell.SetContext(context.Background())
	ctx := context.Background()
	if err := c.Runtime.OpenUserDB(ctx); err != nil {
		t.Fatalf("open user db: %v", err)
	}
	defer c.Runtime.Manager().CloseUserDB()

	NewDiagnosticsBinding(c).ReportFrontendPerf([]FrontendPerfSample{
		{
			Name: "dom_nodes", Value: 10, Unit: "1",
			Labels: map[string]string{
				"surface":         "main",
				"route":           "chat",
				"build":           "0.5.3",
				"conversation_id": "c-1",
				"interaction":     "resume",
				"evil":            "drop me",
				"unit":            "forged",
			},
		},
		{
			Name: "frames", Value: 2, Unit: "1",
			Labels: map[string]string{"surface": strings.Repeat("x", 200)},
		},
	})

	store := c.Runtime.Manager().MetricsStore()
	nodes, err := store.Range(ctx, "frontend.dom_nodes", 0, 0, 10)
	if err != nil {
		t.Fatalf("range dom_nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("dom_nodes samples = %d, want 1", len(nodes))
	}
	attrs := nodes[0].Attrs
	if attrs["surface"] != "main" || attrs["build"] != "0.5.3" ||
		attrs["route"] != "chat" || attrs["conversation_id"] != "c-1" ||
		attrs["interaction"] != "resume" || attrs["unit"] != "1" {
		t.Fatalf("dom_nodes attrs = %v", attrs)
	}
	if _, ok := attrs["evil"]; ok {
		t.Fatalf("unknown label key must be dropped, attrs = %v", attrs)
	}
	frames, err := store.Range(ctx, "frontend.frames", 0, 0, 10)
	if err != nil {
		t.Fatalf("range frames: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames samples = %d, want 1", len(frames))
	}
	if got := frames[0].Attrs["surface"]; len(got) != perfLabelValueMax {
		t.Fatalf("surface label must be capped at %d runes, got %d: %q",
			perfLabelValueMax, len(got), got)
	}
	if frames[0].Ts != nodes[0].Ts {
		t.Fatalf("one report must share one ts, got %d and %d",
			frames[0].Ts, nodes[0].Ts)
	}
}

// TestReportFrontendPerfLogsOneLinePerBatch pins the log hygiene: the probe
// reports a dozen samples every 30 seconds, and one line per sample turned
// the shell log into a telemetry dump of exactly one shape (2294 lines in
// 13 days). The batch is logged once, with its numbers still readable.
func TestReportFrontendPerfLogsOneLinePerBatch(t *testing.T) {
	dir := t.TempDir()
	c := core.NewCore(dir, dir, "")
	c.Shell.SetContext(context.Background())
	capture := logcapture.Install(t)

	NewDiagnosticsBinding(c).ReportFrontendPerf([]FrontendPerfSample{
		{Name: "dom_nodes", Value: 10, Unit: "1"},
		{Name: "frame_max", Value: 255, Unit: "ms"},
		{Name: "interaction_max", Value: 12.5, Unit: "ms"},
	})

	lines := 0
	samples := ""
	for _, record := range capture.Records() {
		body := record.Body().AsString()
		if !strings.HasPrefix(body, "frontend rum:") {
			continue
		}
		lines++
		if body != "frontend rum: report" {
			t.Fatalf("body = %q", body)
		}
		samples = logcapture.Attribute(record, "samples")
	}
	if lines != 1 {
		t.Fatalf("frontend rum log lines = %d, want one batch line", lines)
	}
	if want := "dom_nodes=10 frame_max=255 interaction_max=12.5"; samples != want {
		t.Fatalf("samples = %q, want %q", samples, want)
	}
}
