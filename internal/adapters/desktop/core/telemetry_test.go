package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
)

// newTestPipeline starts a pipeline that only writes to a temp log file,
// which is what a desktop process without OTLP configuration installs.
func newTestPipeline(t *testing.T) *octelemetry.Pipeline {
	t.Helper()
	return startTestPipeline(t, octelemetry.TelemetryOptions{})
}

// newTestPipelineWithAppSink starts a pipeline whose export target came
// from the application configuration (OTEL_EXPORTER_OTLP_ENDPOINT).
func newTestPipelineWithAppSink(t *testing.T, endpoint string) *octelemetry.Pipeline {
	t.Helper()
	return startTestPipeline(t, octelemetry.TelemetryOptions{
		OTLPEndpoint: endpoint,
		OTLPHeaders:  map[string]string{"Authorization": "Bearer app"},
	})
}

func startTestPipeline(
	t *testing.T,
	opts octelemetry.TelemetryOptions,
) *octelemetry.Pipeline {
	t.Helper()
	opts.LogFile = filepath.Join(t.TempDir(), "logs", "opencraft.log")
	p, err := octelemetry.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("start telemetry pipeline: %v", err)
	}
	t.Cleanup(func() {
		// The tests point their OTLP sinks at unreachable collectors
		// (collector.example and friends) on purpose: the sink state,
		// not the delivery, is under test. An unbounded shutdown makes
		// the SDK walk the exporter's retry budget and adds 30-60s per
		// test to the suite, so bound the drain and let the test binary
		// exit drop anything still queued.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})
	return p
}

// newTelemetryTestCore builds a desktop core with the plugin telemetry
// pipeline attached, plus the audit trail it writes.
func newTelemetryTestCore(
	t *testing.T,
	plugins map[string][]string,
) (*Core, string) {
	t.Helper()
	return newTelemetryTestCoreWith(t, plugins, nil)
}

// newTelemetryTestCoreWith is newTelemetryTestCore with an explicit
// pipeline, for tests that need an application-level export sink.
func newTelemetryTestCoreWith(
	t *testing.T,
	plugins map[string][]string,
	pipeline *octelemetry.Pipeline,
) (*Core, string) {
	t.Helper()
	dataDir := t.TempDir()
	for id, perms := range plugins {
		writeTestPlugin(t, dataDir, id, perms)
	}
	c := NewCore(t.TempDir(), dataDir, "")
	if pipeline == nil {
		pipeline = newTestPipeline(t)
	}
	c.Telemetry = pipeline
	return c, dataDir
}

func readTelemetryAudit(t *testing.T, dataDir string) []octelemetry.AuditEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "audit", "telemetry.jsonl"))
	if err != nil {
		t.Fatalf("read audit trail: %v", err)
	}
	if strings.Contains(string(raw), "Bearer") {
		t.Fatalf("audit trail leaked a header value:\n%s", raw)
	}
	var entries []octelemetry.AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var entry octelemetry.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode audit line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestPluginTelemetryRequiresPermission(t *testing.T) {
	c, dataDir := newTelemetryTestCore(t, map[string][]string{
		"plug": {"storage:kv"},
	})

	err := c.handlePluginTelemetryConfigure("plug", pluginruntime.TelemetryExportRequest{
		Endpoint: "collector.example:4318",
	})
	if err == nil || !strings.Contains(err.Error(), "lacks telemetry:export") {
		t.Fatalf("error = %v, want permission error", err)
	}
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink = %+v owner %q, want none installed", sink, owner)
	}
	entries := readTelemetryAudit(t, dataDir)
	if len(entries) != 1 || entries[0].Action != octelemetry.AuditDeny ||
		entries[0].PluginID != "plug" {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestPluginTelemetryRespectsUserSwitch(t *testing.T) {
	c, dataDir := newTelemetryTestCore(t, map[string][]string{
		"plug": {"telemetry:export"},
	})
	if err := c.Shell.SetPluginTelemetryExport(false); err != nil {
		t.Fatalf("disable plugin telemetry: %v", err)
	}

	err := c.handlePluginTelemetryConfigure("plug", pluginruntime.TelemetryExportRequest{
		Endpoint: "collector.example:4318",
	})
	if err == nil || !strings.Contains(err.Error(), "disabled in settings") {
		t.Fatalf("error = %v, want the settings switch error", err)
	}
	if sink, _ := c.Telemetry.Sink(); sink.Endpoint != "" {
		t.Fatalf("sink = %+v, want none installed", sink)
	}
	entries := readTelemetryAudit(t, dataDir)
	if len(entries) != 1 || entries[0].Reason != "plugin telemetry export is disabled in settings" {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestPluginTelemetrySinkLifecycle(t *testing.T) {
	c, dataDir := newTelemetryTestCore(t, map[string][]string{
		"plug-a": {"telemetry:export"},
		"plug-b": {"telemetry:export"},
	})

	// An endpoint with credentials must be rejected without touching the
	// pipeline.
	if err := c.handlePluginTelemetryConfigure("plug-a",
		pluginruntime.TelemetryExportRequest{
			Endpoint: "user:pass@collector.example:4318",
		}); err == nil ||
		!strings.Contains(err.Error(), "credentials in headers") {
		t.Fatalf("error = %v, want the credential rejection", err)
	}
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink = %+v owner %q, want none installed", sink, owner)
	}

	if err := c.handlePluginTelemetryConfigure("plug-a",
		pluginruntime.TelemetryExportRequest{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"authorization": "Bearer a"},
		}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	sink, owner := c.Telemetry.Sink()
	if owner != "plug-a" || sink.Endpoint != "collector.example:4318" {
		t.Fatalf("sink = %+v owner %q", sink, owner)
	}
	if got := sink.Headers["Authorization"]; got != "Bearer a" {
		t.Fatalf("header = %q, want the canonicalized name to keep its value", got)
	}
	status := c.PluginTelemetryStatus()
	if status["owner"] != "plug-a" || status["endpoint"] != "collector.example:4318" {
		t.Fatalf("status = %+v", status)
	}
	if _, leaked := status["headers"]; leaked {
		t.Fatalf("status leaked header values: %+v", status)
	}

	// A second plugin cannot take the sink over while the owner is
	// running: the host keeps exactly one, and takeover is refused.
	err := c.handlePluginTelemetryConfigure("plug-b",
		pluginruntime.TelemetryExportRequest{Endpoint: "127.0.0.1:4318"})
	if err == nil || !strings.Contains(err.Error(), `owned by plugin "plug-a"`) {
		t.Fatalf("takeover error = %v", err)
	}
	if sink, owner := c.Telemetry.Sink(); owner != "plug-a" ||
		sink.Endpoint != "collector.example:4318" {
		t.Fatalf("refused takeover changed the sink: %+v owner %q", sink, owner)
	}

	// A plugin that does not own the sink cannot clear it.
	if err := c.handlePluginTelemetryDisable("plug-b"); err != nil {
		t.Fatalf("disable by non-owner: %v", err)
	}
	if _, owner := c.Telemetry.Sink(); owner != "plug-a" {
		t.Fatalf("owner = %q, want plug-a", owner)
	}

	// A crash drops the sink: the host can no longer talk to the plugin.
	c.handlePluginProcessExit("plug-a")
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink after crash = %+v owner %q, want the application sink",
			sink, owner)
	}

	// The freed sink can be claimed again.
	if err := c.handlePluginTelemetryConfigure("plug-b",
		pluginruntime.TelemetryExportRequest{Endpoint: "127.0.0.1:4318"}); err != nil {
		t.Fatalf("configure after crash: %v", err)
	}
	sink, owner = c.Telemetry.Sink()
	if owner != "plug-b" || sink.Endpoint != "127.0.0.1:4318" || !sink.Insecure {
		t.Fatalf("sink = %+v owner %q", sink, owner)
	}
	if err := c.RemovePluginTelemetry("plug-b"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink = %+v owner %q, want the application sink", sink, owner)
	}
	// Removing it twice is a no-op.
	if err := c.RemovePluginTelemetry("plug-b"); err != nil {
		t.Fatalf("second remove: %v", err)
	}

	entries := readTelemetryAudit(t, dataDir)
	var actions []string
	for _, entry := range entries {
		actions = append(actions, entry.Action)
	}
	want := []string{
		octelemetry.AuditDeny,    // credentials in the endpoint
		octelemetry.AuditInstall, // plug-a
		octelemetry.AuditDeny,    // plug-b takeover refused
		octelemetry.AuditRemove,  // plug-a crashed
		octelemetry.AuditInstall, // plug-b
		octelemetry.AuditRemove,  // plug-b removed
	}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions = %v, want %v", actions, want)
	}
	install := entries[1]
	if install.PluginID != "plug-a" || install.Endpoint != "collector.example:4318" ||
		len(install.HeaderNames) != 1 || install.HeaderNames[0] != "Authorization" {
		t.Fatalf("install entry = %+v", install)
	}
}

func TestPluginTelemetryRejectsWhenAppSinkConfigured(t *testing.T) {
	c, dataDir := newTelemetryTestCoreWith(t,
		map[string][]string{"plug": {"telemetry:export"}},
		newTestPipelineWithAppSink(t, "app-collector.example:4318"))

	err := c.handlePluginTelemetryConfigure("plug", pluginruntime.TelemetryExportRequest{
		Endpoint: "plugin-collector.example:4318",
	})
	if err == nil ||
		!strings.Contains(err.Error(), "application-level OTLP export is configured") {
		t.Fatalf("error = %v, want the application-sink rejection", err)
	}
	sink, owner := c.Telemetry.Sink()
	if owner != "" || sink.Endpoint != "app-collector.example:4318" {
		t.Fatalf("sink = %+v owner %q, want the application sink untouched", sink, owner)
	}
	if got := sink.Headers["Authorization"]; got != "Bearer app" {
		t.Fatalf("application header = %q", got)
	}
	entries := readTelemetryAudit(t, dataDir)
	if len(entries) != 1 || entries[0].Action != octelemetry.AuditDeny {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestPluginTelemetrySwitchRestoresSink(t *testing.T) {
	c, dataDir := newTelemetryTestCore(t, map[string][]string{
		"plug": {"telemetry:export"},
	})
	if err := c.handlePluginTelemetryConfigure("plug",
		pluginruntime.TelemetryExportRequest{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"authorization": "Bearer plug"},
		}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	if err := c.SetPluginTelemetryExport(false); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink = %+v owner %q, want the application sink", sink, owner)
	}
	if c.Shell.PluginTelemetryExport() {
		t.Fatal("switch off was not persisted")
	}

	// The plugin does not re-configure by itself, so turning the switch
	// back on has to restore what the host remembered.
	if err := c.SetPluginTelemetryExport(true); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	sink, owner := c.Telemetry.Sink()
	if owner != "plug" || sink.Endpoint != "collector.example:4318" {
		t.Fatalf("sink = %+v owner %q, want the restored plugin sink", sink, owner)
	}
	if got := sink.Headers["Authorization"]; got != "Bearer plug" {
		t.Fatalf("restored header = %q", got)
	}

	entries := readTelemetryAudit(t, dataDir)
	var actions []string
	for _, entry := range entries {
		actions = append(actions, entry.Action)
	}
	want := []string{
		octelemetry.AuditInstall,
		octelemetry.AuditRemove,
		octelemetry.AuditInstall,
	}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions = %v, want %v", actions, want)
	}
	restored := entries[len(entries)-1]
	if !strings.Contains(restored.Reason, "re-enabled") ||
		restored.PluginID != "plug" {
		t.Fatalf("restore entry = %+v", restored)
	}
}

func TestPluginTelemetrySwitchSkipsRestoreAfterPluginExit(t *testing.T) {
	c, _ := newTelemetryTestCore(t, map[string][]string{
		"plug": {"telemetry:export"},
	})
	if err := c.handlePluginTelemetryConfigure("plug",
		pluginruntime.TelemetryExportRequest{
			Endpoint: "collector.example:4318",
		}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := c.SetPluginTelemetryExport(false); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	// A plugin that dies while export is switched off must not come back
	// with the switch: nothing is exporting on its behalf any more.
	c.handlePluginProcessExit("plug")
	if err := c.SetPluginTelemetryExport(true); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink = %+v owner %q, want nothing restored", sink, owner)
	}

	// The host-owned switch itself still works.
	if !c.Shell.PluginTelemetryExport() {
		t.Fatal("switch on was not persisted")
	}
}

func TestPluginTelemetrySwitchRemembersBlockedRequest(t *testing.T) {
	c, _ := newTelemetryTestCore(t, map[string][]string{
		"plug":   {"telemetry:export"},
		"rival":  {"telemetry:export"},
		"nobody": {"telemetry:export"},
	})
	if err := c.Shell.SetPluginTelemetryExport(false); err != nil {
		t.Fatalf("switch off: %v", err)
	}

	// The plugin asks for export while the user switch blocks it. The
	// request is refused, but re-enabling export must honour it: the
	// plugin has no reason to ask again.
	err := c.handlePluginTelemetryConfigure("plug",
		pluginruntime.TelemetryExportRequest{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"authorization": "Bearer blocked"},
		})
	if err == nil || !strings.Contains(err.Error(), "disabled in settings") {
		t.Fatalf("error = %v, want the settings switch error", err)
	}
	if sink, owner := c.Telemetry.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("sink = %+v owner %q, want nothing installed", sink, owner)
	}

	// Another plugin cannot take the suspended slot over while the
	// switch is off; its request is refused like any other.
	err = c.handlePluginTelemetryConfigure("rival",
		pluginruntime.TelemetryExportRequest{
			Endpoint: "rival-collector.example:4318",
		})
	if err == nil || !strings.Contains(err.Error(), "disabled in settings") {
		t.Fatalf("rival error = %v, want the settings switch error", err)
	}

	if err := c.SetPluginTelemetryExport(true); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	sink, owner := c.Telemetry.Sink()
	if owner != "plug" || sink.Endpoint != "collector.example:4318" {
		t.Fatalf("sink = %+v owner %q, want plug's blocked request applied", sink, owner)
	}
	if got := sink.Headers["Authorization"]; got != "Bearer blocked" {
		t.Fatalf("restored header = %q", got)
	}
}
