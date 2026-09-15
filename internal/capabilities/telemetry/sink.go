package telemetry

import (
	"fmt"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
)

// Bounds on one externally-supplied export sink. The host installs at
// most one such sink, so the limits only have to keep a single header
// block and endpoint sane; they exist so a plugin cannot turn the
// exporter configuration into an unbounded transport.
const (
	maxSinkEndpointLen = 512
	maxSinkHeaders     = 16
	maxSinkHeaderName  = 128
	maxSinkHeaderValue = 4096
)

// Sink is one OTLP export target: the collector endpoint plus the
// headers sent with every export request. It is the unit a capability
// plugin configures through the telemetry.configure primitive.
type Sink struct {
	// Endpoint is the collector host[:port]. A leading http:// or
	// https:// scheme is accepted and stripped; plaintext endpoints
	// are restricted to loopback hosts (see Validate).
	Endpoint string
	// Headers carries collector credentials. Values are secrets: the
	// host keeps them in memory for the lifetime of the process,
	// never writes them to disk, and logs header names only.
	Headers map[string]string
	// Insecure requests plaintext OTLP for an endpoint written
	// without a scheme. It is only honored for loopback hosts.
	Insecure bool
}

// forbiddenSinkHeaders are headers the OTLP exporter owns or that would
// break the HTTP exchange if a caller replaced them.
var forbiddenSinkHeaders = map[string]bool{
	"connection":        true,
	"content-encoding":  true,
	"content-length":    true,
	"content-type":      true,
	"host":              true,
	"keep-alive":        true,
	"te":                true,
	"trailer":           true,
	"transfer-encoding": true,
	"upgrade":           true,
}

// Validate reports whether the host may install this sink. Callers must
// validate the sink they received, before Normalize: the rules below
// distinguish "the caller asked for plaintext" from "the scheme implies
// plaintext", and plaintext is only accepted for loopback collectors.
func (s Sink) Validate() error {
	raw := strings.TrimSpace(s.Endpoint)
	if raw == "" {
		return fmt.Errorf("telemetry: sink endpoint is required")
	}
	if len(raw) > maxSinkEndpointLen {
		return fmt.Errorf("telemetry: sink endpoint exceeds %d characters",
			maxSinkEndpointLen)
	}
	scheme := ""
	rest := raw
	if idx := strings.Index(raw, "://"); idx >= 0 {
		scheme = strings.ToLower(raw[:idx])
		rest = raw[idx+3:]
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("telemetry: sink endpoint scheme %q is not supported",
				scheme)
		}
	}
	parsed, err := url.Parse("https://" + rest)
	if err != nil {
		return fmt.Errorf("telemetry: sink endpoint %q: %w", raw, err)
	}
	if parsed.User != nil {
		return fmt.Errorf("telemetry: sink endpoint must carry its credentials in headers")
	}
	if parsed.Host == "" {
		return fmt.Errorf("telemetry: sink endpoint %q has no host", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("telemetry: sink endpoint must not carry a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("telemetry: sink endpoint must not carry a query or fragment")
	}
	plaintext := scheme == "http" || (scheme == "" && s.Insecure)
	if plaintext && !isLoopback(parsed.Host) {
		return fmt.Errorf(
			"telemetry: plaintext OTLP is only allowed for loopback endpoints; use https for %q",
			parsed.Host)
	}
	if len(s.Headers) > maxSinkHeaders {
		return fmt.Errorf("telemetry: sink carries %d headers, limit is %d",
			len(s.Headers), maxSinkHeaders)
	}
	for name, value := range s.Headers {
		trimmed := strings.TrimSpace(name)
		switch {
		case trimmed == "":
			return fmt.Errorf("telemetry: sink header name is empty")
		case len(trimmed) > maxSinkHeaderName:
			return fmt.Errorf("telemetry: sink header %q name exceeds %d characters",
				trimmed, maxSinkHeaderName)
		case !validHeaderName(trimmed):
			return fmt.Errorf("telemetry: sink header name %q is not a valid token",
				trimmed)
		case forbiddenSinkHeaders[strings.ToLower(trimmed)]:
			return fmt.Errorf("telemetry: sink header %q is reserved for the exporter",
				trimmed)
		case len(value) > maxSinkHeaderValue:
			return fmt.Errorf("telemetry: sink header %q value exceeds %d characters",
				trimmed, maxSinkHeaderValue)
		case strings.ContainsAny(value, "\r\n"):
			return fmt.Errorf("telemetry: sink header %q value must not contain CR or LF",
				trimmed)
		}
	}
	return nil
}

// Normalize canonicalizes a sink for installation: the endpoint loses
// its scheme (and picks up insecure for loopback/plaintext), header
// names are canonicalized and values trimmed.
func (s Sink) Normalize() Sink {
	endpoint, insecure := normalizeOTLP(strings.TrimSpace(s.Endpoint), s.Insecure)
	out := Sink{Endpoint: endpoint, Insecure: insecure}
	if len(s.Headers) > 0 {
		out.Headers = make(map[string]string, len(s.Headers))
		for name, value := range s.Headers {
			out.Headers[textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))] =
				strings.TrimSpace(value)
		}
	}
	return out
}

// Clone returns a copy whose header map is safe to hand out.
func (s Sink) Clone() Sink {
	out := Sink{Endpoint: s.Endpoint, Insecure: s.Insecure}
	if len(s.Headers) > 0 {
		out.Headers = make(map[string]string, len(s.Headers))
		for name, value := range s.Headers {
			out.Headers[name] = value
		}
	}
	return out
}

// HeaderNames returns the sorted header names of the sink. It never
// exposes values: header values are credentials, header names are
// diagnostic.
func (s Sink) HeaderNames() []string {
	if len(s.Headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.Headers))
	for name := range s.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validHeaderName reports whether name is an RFC 7230 token.
func validHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		if !tokenByte(name[i]) {
			return false
		}
	}
	return true
}

func tokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
