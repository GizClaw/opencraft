package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	coretelemetry "github.com/GizClaw/flowcraft/core/telemetry"
)

// pipelineDrainTimeout bounds the flush of a pipeline that is being
// replaced. It is deliberately independent of the caller's context,
// which a Wails shutdown or a finished RPC may already have canceled.
const pipelineDrainTimeout = 5 * time.Second

// Pipeline owns the process-wide OTel providers installed by InitOtel
// and supports swapping the OTLP export sink at runtime: a capability
// plugin configures its own collector while the rotating file sink
// configured at startup stays in place.
//
// The options passed to Start are the base (application) configuration.
// Reset returns the process to them, which is what dropping a plugin
// sink does. Sinks are installed one at a time and installing the next
// one happens before the previous pipeline is torn down, so export
// never stops mid-swap; a rejected sink leaves the active pipeline
// untouched.
type Pipeline struct {
	mu       sync.Mutex
	base     TelemetryOptions
	sink     Sink
	owner    string
	shutdown func(context.Context) error
}

// Start installs the base pipelines and returns their owner. A nil
// pipeline is never returned: init failures are reported so the caller
// can decide whether to run without telemetry.
func Start(ctx context.Context, base TelemetryOptions) (*Pipeline, error) {
	shutdown, err := InitOtel(ctx, base)
	if err != nil {
		return nil, err
	}
	return &Pipeline{
		base:     base,
		sink:     sinkFromOptions(base),
		shutdown: shutdown,
	}, nil
}

// Reconfigure installs sink as the active OTLP export target. owner
// names the caller that asked for it (a plugin id, or "" for the
// application), which is how the host later clears a sink whose plugin
// is disabled or uninstalled.
func (p *Pipeline) Reconfigure(ctx context.Context, sink Sink, owner string) error {
	if err := sink.Validate(); err != nil {
		return err
	}
	return p.install(ctx, sink.Normalize(), owner)
}

// install swaps the active pipeline without validating the sink: the
// application-level configuration (and the empty sink that disables
// export) does not go through the plugin-facing rules.
func (p *Pipeline) install(ctx context.Context, sink Sink, owner string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	shutdown, err := InitOtel(ctx, p.options(sink))
	if err != nil {
		return fmt.Errorf("telemetry: install sink %q: %w", sink.Endpoint, err)
	}
	previous := p.shutdown
	p.shutdown = shutdown
	p.sink = sink
	p.owner = owner
	p.drain(previous)
	return nil
}

// Reset returns the process to the application-level sink. It is a
// no-op when the application sink is already active.
func (p *Pipeline) Reset(ctx context.Context) error {
	if _, owner := p.Sink(); owner == "" {
		return nil
	}
	return p.install(ctx, sinkFromOptions(p.baseOptions()), "")
}

// Sink returns the active OTLP sink and its owner ("" when the
// application configuration owns it). The returned sink is a copy.
func (p *Pipeline) Sink() (Sink, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sink.Clone(), p.owner
}

// Shutdown flushes and tears down the active pipelines. It is safe to
// call more than once.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	shutdown := p.shutdown
	p.shutdown = nil
	p.mu.Unlock()
	if shutdown == nil {
		return nil
	}
	return shutdown(ctx)
}

// options merges the file sink from the base configuration with one
// OTLP sink. The file sink belongs to the application, so a plugin can
// only ever replace the export target, never the local log file.
func (p *Pipeline) options(sink Sink) TelemetryOptions {
	base := p.baseOptions()
	return TelemetryOptions{
		OTLPEndpoint: sink.Endpoint,
		OTLPHeaders:  sink.Headers,
		OTLPInsecure: sink.Insecure,
		LogFile:      base.LogFile,
	}
}

// baseOptions returns the immutable startup options. The mutex is not
// needed: base is written once, before the pipeline is published.
func (p *Pipeline) baseOptions() TelemetryOptions {
	return p.base
}

// drain flushes a replaced pipeline.
func (p *Pipeline) drain(shutdown func(context.Context) error) {
	if shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pipelineDrainTimeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		coretelemetry.WarnErr(ctx, "telemetry: drain replaced pipeline failed", err)
	}
}

// sinkFromOptions projects the application-level export configuration
// onto the sink shape.
func sinkFromOptions(opts TelemetryOptions) Sink {
	return Sink{
		Endpoint: opts.OTLPEndpoint,
		Headers:  opts.OTLPHeaders,
		Insecure: opts.OTLPInsecure,
	}.Normalize()
}
