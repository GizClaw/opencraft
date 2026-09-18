package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
)

// pluginTelemetryTimeout bounds one sink swap. Installing the pipeline
// is fast; the budget exists so a wedged exporter cannot hold the
// plugin's RPC open indefinitely.
const pluginTelemetryTimeout = 10 * time.Second

// rememberedPluginSink is the plugin sink the host dropped because the
// user switch went off. The switch restores it when it comes back on, so
// a plugin that configured export once is not silently left without a
// collector. It is memory-only: an app restart falls back to the
// application configuration, and the plugin re-configures when it runs.
type rememberedPluginSink struct {
	pluginID string
	sink     octelemetry.Sink
}

// wirePluginTelemetry routes capability-plugin OTLP export requests into
// the shared telemetry pipeline. The host owns the policy: one active
// plugin sink at a time, gated by the declaring permission and the user
// switch, validated before the live pipeline is touched, and dropped
// when the owning plugin is disabled or uninstalled.
func (c *Core) wirePluginTelemetry() {
	if c.Plugin == nil || c.Plugin.Capability == nil {
		return
	}
	c.Plugin.Capability.SetTelemetryHandler(pluginruntime.TelemetryHandler{
		Configure: c.handlePluginTelemetryConfigure,
		Disable:   c.handlePluginTelemetryDisable,
	})
	c.Plugin.Capability.SetProcessExitHandler(c.handlePluginProcessExit)
}

// handlePluginTelemetryConfigure installs the OTLP sink one capability
// plugin asked for. Header values are collector credentials: they stay
// in host memory (the pipeline drops them when the sink is replaced) and
// only header names are logged.
func (c *Core) handlePluginTelemetryConfigure(
	pluginID string,
	req pluginruntime.TelemetryExportRequest,
) error {
	if !c.pluginHasPermission(pluginID, "telemetry:export") {
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Reason:   "missing telemetry:export permission",
		})
		return fmt.Errorf(
			"telemetry.configure: plugin %q lacks telemetry:export permission",
			pluginID)
	}
	sink := octelemetry.Sink{
		Endpoint: req.Endpoint,
		Headers:  req.Headers,
		Insecure: req.Insecure,
	}
	validationErr := sink.Validate()
	// A malformed request is rejected as malformed whatever the switch
	// says, and never remembered.
	if validationErr != nil {
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Reason:   validationErr.Error(),
		})
		return fmt.Errorf("telemetry.configure: %w", validationErr)
	}
	if !c.Shell.PluginTelemetryExport() {
		// A request the switch alone blocked is remembered: re-enabling
		// export applies the plugin's latest intent instead of waiting
		// for another configure call that may never come. A different
		// plugin cannot claim the suspended slot: its request stays
		// refused until the owner releases it or disappears.
		if owner, _, ok := c.rememberedPluginTelemetry(); !ok || owner == pluginID {
			c.rememberPluginTelemetry(pluginID, sink.Normalize())
		}
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Reason:   "plugin telemetry export is disabled in settings",
		})
		return errors.New(
			"telemetry.configure: plugin telemetry export is disabled in settings")
	}
	pipeline := c.Telemetry
	if pipeline == nil {
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Reason:   "telemetry pipeline unavailable",
		})
		return errors.New("telemetry.configure: telemetry pipeline unavailable")
	}
	// One sink at a time, and an operator-configured application sink
	// wins over plugins: a plugin may only take over a sink it already
	// owns, or claim a free one.
	active, owner := pipeline.Sink()
	switch {
	case owner == "" && active.Endpoint != "":
		reason := "application-level OTLP export is configured"
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Owner:    owner,
			Reason:   reason,
		})
		return fmt.Errorf(
			"telemetry.configure: %s; plugin export sinks stay disabled", reason)
	case owner != "" && owner != pluginID:
		reason := fmt.Sprintf("export sink is owned by plugin %q", owner)
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Owner:    owner,
			Reason:   reason,
		})
		return fmt.Errorf("telemetry.configure: %s", reason)
	}
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(c.Shell.Context()), pluginTelemetryTimeout)
	defer cancel()
	if err := pipeline.Reconfigure(ctx, sink, pluginID); err != nil {
		c.auditTelemetry(octelemetry.AuditEntry{
			Action:   octelemetry.AuditDeny,
			PluginID: pluginID,
			Endpoint: req.Endpoint,
			Reason:   err.Error(),
		})
		return err
	}
	normalized := sink.Normalize()
	c.rememberPluginTelemetry(pluginID, normalized)
	// Installing a sink is a configuration event the user asked for,
	// not a warning: it repeats on every plugin reconnect.
	telemetry.Info(ctx, "plugin telemetry: OTLP export sink installed",
		otellog.String("plugin.id", pluginID),
		otellog.String("telemetry.endpoint", normalized.Endpoint),
		otellog.Bool("telemetry.insecure", normalized.Insecure),
		otellog.String("telemetry.header_names",
			strings.Join(normalized.HeaderNames(), ",")))
	c.auditTelemetry(octelemetry.AuditEntry{
		Action:      octelemetry.AuditInstall,
		PluginID:    pluginID,
		Endpoint:    normalized.Endpoint,
		Insecure:    normalized.Insecure,
		HeaderNames: normalized.HeaderNames(),
		Owner:       pluginID,
	})
	c.Shell.Emit("telemetry_changed", c.PluginTelemetryStatus())
	return nil
}

// handlePluginTelemetryDisable drops the sink when the calling plugin
// owns it. A plugin that does not own the active sink is ignored rather
// than rejected, so cleanup paths stay idempotent.
func (c *Core) handlePluginTelemetryDisable(pluginID string) error {
	c.forgetPluginTelemetry(pluginID)
	return c.dropPluginTelemetry(pluginID, "plugin removed its export sink")
}

// handlePluginProcessExit drops the export sink of a capability plugin
// that died on its own (crash or kill): a process the host can no longer
// talk to must not keep shipping logs to its collector. The host-stopped
// paths (disable, uninstall) clear it explicitly and do not fire this.
func (c *Core) handlePluginProcessExit(pluginID string) {
	c.forgetPluginTelemetry(pluginID)
	if err := c.dropPluginTelemetry(pluginID, "plugin process exited"); err != nil {
		telemetry.WarnErr(context.Background(),
			"plugin telemetry: clear sink after plugin exit failed", err,
			otellog.String("plugin.id", pluginID))
	}
}

// RemovePluginTelemetry returns the pipeline to the application-level
// export configuration when pluginID owns the active sink. It is the
// host-side fallback used when a plugin is disabled, updated or
// uninstalled, mirroring RemovePluginInference.
func (c *Core) RemovePluginTelemetry(pluginID string) error {
	c.forgetPluginTelemetry(pluginID)
	return c.dropPluginTelemetry(pluginID, "plugin disabled or uninstalled")
}

// SetPluginTelemetryExport applies the user switch. Turning it off drops
// the active plugin sink but remembers it, so turning the switch back on
// re-installs it instead of leaving the plugin without a collector.
func (c *Core) SetPluginTelemetryExport(enabled bool) error {
	if err := c.Shell.SetPluginTelemetryExport(enabled); err != nil {
		return err
	}
	if enabled {
		return c.restorePluginTelemetry()
	}
	owner := c.PluginTelemetryState().Owner
	if owner == "" {
		c.Shell.Emit("telemetry_changed", c.PluginTelemetryStatus())
		return nil
	}
	return c.dropPluginTelemetry(owner,
		"plugin telemetry export disabled in settings")
}

// restorePluginTelemetry re-installs the sink the switch suspended. The
// sink is gone from memory when its plugin was disabled, uninstalled or
// died, so nothing is restored on behalf of a plugin that is gone.
func (c *Core) restorePluginTelemetry() error {
	pluginID, sink, ok := c.rememberedPluginTelemetry()
	if !ok || c.Telemetry == nil {
		c.Shell.Emit("telemetry_changed", c.PluginTelemetryStatus())
		return nil
	}
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(c.Shell.Context()), pluginTelemetryTimeout)
	defer cancel()
	if err := c.Telemetry.Reconfigure(ctx, sink, pluginID); err != nil {
		return err
	}
	telemetry.Warn(ctx, "plugin telemetry: export sink restored",
		otellog.String("plugin.id", pluginID),
		otellog.String("telemetry.endpoint", sink.Endpoint))
	c.auditTelemetry(octelemetry.AuditEntry{
		Action:      octelemetry.AuditInstall,
		PluginID:    pluginID,
		Endpoint:    sink.Endpoint,
		Insecure:    sink.Insecure,
		HeaderNames: sink.HeaderNames(),
		Owner:       pluginID,
		Reason:      "restored after the plugin export switch was re-enabled",
	})
	c.Shell.Emit("telemetry_changed", c.PluginTelemetryStatus())
	return nil
}

// dropPluginTelemetry clears the active sink owned by pluginID. It keeps
// the remembered copy, which is what the user switch needs.
func (c *Core) dropPluginTelemetry(pluginID, reason string) error {
	pipeline := c.Telemetry
	if pipeline == nil {
		return nil
	}
	active, owner := pipeline.Sink()
	if owner != pluginID {
		return nil
	}
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(c.Shell.Context()), pluginTelemetryTimeout)
	defer cancel()
	if err := pipeline.Reset(ctx); err != nil {
		return err
	}
	telemetry.Warn(ctx, "plugin telemetry: export sink removed",
		otellog.String("plugin.id", pluginID),
		otellog.String("telemetry.reason", reason))
	c.auditTelemetry(octelemetry.AuditEntry{
		Action:      octelemetry.AuditRemove,
		PluginID:    pluginID,
		Endpoint:    active.Endpoint,
		Insecure:    active.Insecure,
		HeaderNames: active.HeaderNames(),
		Owner:       owner,
		Reason:      reason,
	})
	c.Shell.Emit("telemetry_changed", c.PluginTelemetryStatus())
	return nil
}

// rememberPluginTelemetry records the sink a plugin installed, for the
// switch to restore after it was turned off and on again.
func (c *Core) rememberPluginTelemetry(pluginID string, sink octelemetry.Sink) {
	c.telemetryMu.Lock()
	defer c.telemetryMu.Unlock()
	c.telemetryLast = &rememberedPluginSink{
		pluginID: pluginID,
		sink:     sink.Clone(),
	}
}

// forgetPluginTelemetry drops the remembered sink when it belongs to
// pluginID. A different plugin's entry is left alone so a delayed exit
// notification cannot erase the current owner's sink.
func (c *Core) forgetPluginTelemetry(pluginID string) {
	c.telemetryMu.Lock()
	defer c.telemetryMu.Unlock()
	if c.telemetryLast != nil && c.telemetryLast.pluginID == pluginID {
		c.telemetryLast = nil
	}
}

func (c *Core) rememberedPluginTelemetry() (string, octelemetry.Sink, bool) {
	c.telemetryMu.Lock()
	defer c.telemetryMu.Unlock()
	if c.telemetryLast == nil {
		return "", octelemetry.Sink{}, false
	}
	return c.telemetryLast.pluginID, c.telemetryLast.sink.Clone(), true
}

// auditTelemetry appends one export-sink entry to the app-level audit
// trail. Audit writes are best-effort and never block the caller.
func (c *Core) auditTelemetry(entry octelemetry.AuditEntry) {
	octelemetry.AppendAudit(filepath.Join(c.DataDir, "audit"), entry)
}

// PluginTelemetryState is the typed status of the plugin export sink.
// Header values are credentials and never leave the host, so only names
// are reported.
type PluginTelemetryState struct {
	// Enabled is the user switch (desktop.json telemetry.pluginExport).
	Enabled bool
	// Configured reports whether an OTLP endpoint is active, whoever
	// installed it.
	Configured bool
	Endpoint   string
	Insecure   bool
	// HeaderNames names the header entries the sink sends; values stay
	// in the pipeline.
	HeaderNames []string
	// Owner is the plugin that installed the sink, empty when the
	// application configuration owns it.
	Owner string
}

// PluginTelemetryState reports the active OTLP export sink.
func (c *Core) PluginTelemetryState() PluginTelemetryState {
	pipeline := c.Telemetry
	if pipeline == nil {
		return PluginTelemetryState{
			Enabled: c.Shell.PluginTelemetryExport(),
		}
	}
	sink, owner := pipeline.Sink()
	return PluginTelemetryState{
		Enabled:     c.Shell.PluginTelemetryExport(),
		Configured:  sink.Endpoint != "",
		Endpoint:    sink.Endpoint,
		Insecure:    sink.Insecure,
		HeaderNames: sink.HeaderNames(),
		Owner:       owner,
	}
}

// PluginTelemetryStatus renders PluginTelemetryState for the UI event
// emitted when the sink changes.
func (c *Core) PluginTelemetryStatus() map[string]any {
	state := c.PluginTelemetryState()
	return map[string]any{
		"configured":   state.Configured,
		"enabled":      state.Enabled,
		"endpoint":     state.Endpoint,
		"insecure":     state.Insecure,
		"header_names": state.HeaderNames,
		"owner":        state.Owner,
	}
}
