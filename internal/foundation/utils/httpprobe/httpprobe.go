// Package httpprobe installs an opt-in round-trip probe on the
// process-wide HTTP transport.
//
// It answers one question the per-turn latency reports could not: how
// much of a slow inference step is local assembly (engine bookkeeping,
// request encoding, connection setup) and how much is the provider
// (upload, time to first byte, generation). The provider drivers log the
// response boundary ("inference stream opened") and nothing before it, so
// "tool result -> next request" is a single number without this probe.
// With it, one round trip emits two records:
//
//	httpprobe: request dispatched        (the request bytes left the process)
//	httpprobe: response headers received  (status + the wait so far)
//
// The gap from the previous host log to "request dispatched" is local
// work; "request dispatched" to "response headers received" is network
// plus provider time to first byte. Both records carry trace_id when the
// caller's context has one, so they join the driver's lines.
//
// The probe is opt-in and its callers own the opt-in: the desktop reload
// path installs it when the diagnostics switch (desktop.json
// diagnostics.httpProbe) or OPENCRAFT_HTTP_PROBE asks for it, and
// Install itself only makes sure the wrapper is in place at most once.
// This is a diagnostic, not a feature, and the log file already records
// every model call twice, so nothing installs it unasked. Only request
// bodies are measured, never logged, and only POST/PUT/PATCH round trips
// are recorded, which keeps the output to provider traffic.
//
// A wrapped process transport is not transparent to every caller.
// flowcraft's core/utils.NewRoundTripper clones http.DefaultTransport
// through an unchecked *http.Transport assertion, and that helper builds
// the streamable-HTTP MCP client and the extractor's fallback client. The
// provider SDKs guard the same assertion and document a wrapped transport
// as supported, so inference traffic is safe either way; the MCP path is
// not, and its client is built during a runtime reload. Uninstall restores
// the transport for those callers, and the desktop reload path reconciles
// the probe against the MCP configuration, so installing it is never a
// choice that has to be remembered later. Clients that already hold the
// wrapper stay correct too: RoundTrip reads the installed flag per call,
// so switching the probe off quiets transports it has handed out.
package httpprobe

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// Env enables the probe. Any value strconv.ParseBool accepts turns it on.
const Env = "OPENCRAFT_HTTP_PROBE"

// installed guards against a second Install wrapping the wrapper.
var installed atomic.Bool

// Enabled reports whether the environment asks for the probe; the
// desktop reload path ORs it with the diagnostics switch (see
// reconcileProbe in adapters/desktop/core).
func Enabled() bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(Env)))
	return err == nil && value
}

// Install wraps http.DefaultTransport so every provider round trip is
// logged at the boundaries above, and reports whether the probe is active
// afterwards. It is idempotent and deliberately policy-free: the caller
// owns the opt-in (the desktop reload path installs it when the switch or
// the env var asks for it and no MCP server is in the way). The provider
// SDKs read http.DefaultTransport when they build their client, so this
// is the only place the whole call can be observed without patching a
// driver.
func Install() bool {
	if installed.CompareAndSwap(false, true) {
		http.DefaultTransport = &transport{base: http.DefaultTransport}
	}
	return true
}

// Active reports whether the probe is installed.
func Active() bool {
	return installed.Load()
}

// Uninstall restores the transport the probe wrapped and reports whether
// one was in place. Callers use it before building a client that cannot
// carry a wrapper (see the package comment); Install puts it back.
func Uninstall() bool {
	if !installed.CompareAndSwap(true, false) {
		return false
	}
	if wrapped, ok := http.DefaultTransport.(*transport); ok {
		http.DefaultTransport = wrapped.base
	}
	return true
}

type transport struct {
	base http.RoundTripper
}

// RoundTrip records the two boundaries while the probe is installed.
// The installed flag is read per call rather than captured when the
// wrapper is handed out: a client built while the probe was on keeps the
// wrapper forever, so switching off has to reach it here.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !installed.Load() || !probed(req) {
		return t.base.RoundTrip(req)
	}
	ctx := req.Context()
	attrs := []otellog.KeyValue{
		otellog.String("http.method", req.Method),
		otellog.String("http.host", req.URL.Host),
		otellog.String("http.path", req.URL.Path),
		otellog.Int64("request_bytes", requestBytes(req)),
	}
	if id := traceID(ctx); id != "" {
		attrs = append(attrs, otellog.String("trace_id", id))
	}
	telemetry.Info(ctx, "httpprobe: request dispatched", attrs...)

	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	waited := time.Since(started)
	if err != nil {
		telemetry.WarnErr(ctx, "httpprobe: request failed",
			fmt.Errorf("%s %s after %s: %w", req.Method, req.URL.Host, waited.Round(time.Millisecond), err),
			attrs...)
		return nil, err
	}
	telemetry.Info(ctx, "httpprobe: response headers received",
		otellog.String("http.status", strconv.Itoa(resp.StatusCode)),
		otellog.Int64("wait_ms", waited.Milliseconds()),
		otellog.Bool("content_length_known", resp.ContentLength >= 0),
		otellog.String("content_type", resp.Header.Get("Content-Type")))
	return resp, nil
}

// probed reports whether one request is worth recording: a body-carrying
// write, which is what every inference call is. GETs (model lists, MCP
// discovery) are cheap and would only add noise, and the app's own OTLP
// export is a POST too, so the collector paths stay out of the record.
func probed(req *http.Request) bool {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return false
	}
	if strings.Contains(req.URL.Path, "/v1/") {
		switch path.Base(req.URL.Path) {
		case "logs", "traces", "metrics":
			return false
		}
	}
	return true
}

// requestBytes reports the declared body size, or -1 when the request
// has no measurable body. The body itself is never read or logged: the
// driver may stream it, and a request body holds conversation content.
func requestBytes(req *http.Request) int64 {
	switch {
	case req.ContentLength > 0:
		return req.ContentLength
	case req.Body != nil:
		return -1
	default:
		return 0
	}
}

// traceID returns the caller's trace id, when the runtime produced one.
func traceID(ctx context.Context) string {
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() {
		return ""
	}
	return span.TraceID().String()
}
