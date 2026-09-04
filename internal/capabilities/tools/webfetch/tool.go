package webfetch

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/extract"
	"golang.org/x/net/http/httpproxy"
)

// Name is the canonical web_fetch tool name.
const Name = "web_fetch"

// Tool is the LLM-callable web_fetch tool. It retrieves an HTTP(S) URL
// and returns the extracted article content (title, description, site
// name, and cleaned text), bounded by timeout and size.
type Tool struct {
	extractor extract.Extractor
	gate      func(context.Context, string) error
	// allowPrivate, when non-nil, is consulted per request to decide
	// whether private/loopback/link-local destinations are permitted
	// (YOLO sessions opt in). Nil means always blocked.
	allowPrivate func(context.Context) bool
	// client is the hardened HTTP client used when a gate is wired: it
	// re-validates every redirect target and pins DNS to the resolved
	// addresses so the SSRF guard cannot be bypassed with a redirect
	// or a DNS rebinding between validation and dial.
	client *http.Client
}

// New creates the web_fetch tool.
func New() *Tool {
	return &Tool{extractor: extract.New()}
}

// SetGate installs the domain policy gate (nil disables it).
func (t *Tool) SetGate(gate func(context.Context, string) error) {
	t.gate = gate
	t.rebuildClient()
}

// SetAllowPrivate installs the per-request resolver that permits
// private destinations. It is consulted only when a gate is wired;
// nil (the default) keeps private destinations blocked.
func (t *Tool) SetAllowPrivate(fn func(context.Context) bool) {
	t.allowPrivate = fn
	t.rebuildClient()
}

func (t *Tool) rebuildClient() {
	if t.gate != nil {
		t.client = hardenedClient(t.gate, t.allowPrivate)
	} else {
		t.client = nil
	}
}

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Fetch an HTTP(S) URL and return the extracted article content: "+
			"title, description, site name, and cleaned main text "+
			"(readability + metadata, whitespace-collapsed). Use for pages "+
			"a search result points to, documentation, or any URL whose "+
			"content you need. Non-http(s) schemes are rejected; response "+
			"size and runtime are bounded.",
		message.ToolProperty("url", "string",
			"The absolute http:// or https:// URL to fetch (required)."),
		message.ToolPropertyWithDefault("max_length", "integer",
			"Maximum characters of text to return (default 8000).",
			8000),
		message.ToolPropertyWithDefault("format", "string",
			"Output format: \"text\" (default) or \"markdown\".",
			"text"),
	).Required("url").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
		Format    string `json:"format"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("web_fetch: parse arguments: %v", err)
	}
	if args.URL == "" {
		return "", errdefs.Validationf("web_fetch: url is required")
	}
	u, err := url.Parse(args.URL)
	if err != nil {
		return "", errdefs.Validationf("web_fetch: parse url: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errdefs.Validationf(
			"web_fetch: unsupported scheme %q", u.Scheme)
	}
	if t.gate != nil {
		if err := t.gate(ctx, u.Hostname()); err != nil {
			return "", err
		}
		// The SSRF guard pins DNS to the validated addresses only on
		// direct dials. Through an HTTP(S) proxy the transport dials the
		// proxy instead, so the target would be re-resolved by the proxy
		// and a DNS rebinding window would reopen. Fail closed: refuse
		// proxy use while the guard is active. YOLO/allow_private paths
		// resolve allowPrivate to true and are exempt (no SSRF concern).
		if t.allowPrivate == nil || !t.allowPrivate(ctx) {
			if proxy, err := envProxy(u.String()); err != nil {
				return "", errdefs.Validationf(
					"web_fetch: proxy resolution: %v", err)
			} else if proxy != nil {
				return "", errdefs.Validationf(
					"web_fetch: the SSRF guard cannot pin DNS through proxy %s; "+
						"unset HTTP(S)_PROXY/ALL_PROXY for this fetch or enable "+
						"web_fetch.allow_private", proxy.Redacted())
			}
		}
	}

	var opts []extract.Option
	if args.MaxLength > 0 {
		opts = append(opts, extract.WithMaxCharacters(args.MaxLength))
	}
	if strings.EqualFold(args.Format, "markdown") {
		opts = append(opts, extract.WithFormat(extract.FormatMarkdown))
	}
	if t.client != nil {
		opts = append(opts, extract.WithHTTPClient(t.client))
	}

	content, err := t.extractor.Extract(ctx, args.URL, opts...)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"url":          content.FinalURL,
		"title":        content.Title,
		"description":  content.Description,
		"site_name":    content.SiteName,
		"content":      content.Content,
		"content_type": content.ContentType,
		"truncated":    content.Truncated,
	})
	if err != nil {
		return "", errdefs.Internalf("web_fetch: encode result: %v", err)
	}
	return string(payload), nil
}

// maxRedirects bounds redirect chains followed by the hardened client.
const maxRedirects = 10

// hardenedClient builds an http.Client whose transport validates the
// destination host on every connection and whose redirect policy
// re-runs the gate for each hop. DNS is resolved once per dial and the
// connection targets the exact validated addresses, closing the
// redirect and DNS-rebinding bypasses of a check that only runs on the
// original URL.
func hardenedClient(
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		// Re-read the environment on every request (httpproxy, unlike
		// http.ProxyFromEnvironment, does not cache the first lookup), so
		// the Execute-side proxy rejection and this transport agree on
		// the current proxy configuration.
		Proxy: func(req *http.Request) (*url.URL, error) {
			return envProxy(req.URL.String())
		},
		DialContext:           pinnedDial(gate, allowPrivate, dialer),
		DialTLSContext:        pinnedTLSDial(gate, allowPrivate, dialer),
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf(
					"web_fetch: stopped after %d redirects", maxRedirects)
			}
			if gate != nil {
				if err := gate(req.Context(), req.URL.Hostname()); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// envProxy resolves the proxy configured for rawURL from the process
// environment. It is a variable so tests can stub it without mutating
// the process environment.
var envProxy = func(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return httpproxy.FromEnvironment().ProxyFunc()(u)
}

// pinnedDial resolves addr once, validates the resolved addresses
// against the SSRF guard, and dials those exact addresses. The gate is
// consulted for the host lists as well, so allow/deny rules keep
// working even when the host is not a private literal.
func pinnedDial(
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
	dialer *net.Dialer,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("web_fetch: parse dial address %q: %w", addr, err)
		}
		if gate != nil {
			if err := gate(ctx, host); err != nil {
				return nil, err
			}
		}
		ips, err := validatedAddresses(ctx, host, allowPrivate != nil && allowPrivate(ctx))
		if err != nil {
			return nil, err
		}
		return dialIPs(ctx, dialer, network, ips, port)
	}
}

// pinnedTLSDial is pinnedDial plus the TLS handshake against the
// hostname, so SNI/certificate validation still uses the original
// host while the connection itself targets the validated address.
func pinnedTLSDial(
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
	dialer *net.Dialer,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("web_fetch: parse dial address %q: %w", addr, err)
		}
		if gate != nil {
			if err := gate(ctx, host); err != nil {
				return nil, err
			}
		}
		ips, err := validatedAddresses(ctx, host, allowPrivate != nil && allowPrivate(ctx))
		if err != nil {
			return nil, err
		}
		conn, err := dialIPs(ctx, dialer, network, ips, port)
		if err != nil {
			return nil, err
		}
		tconn := tls.Client(conn, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tconn.HandshakeContext(ctx); err != nil {
			telemetry.WarnErr(ctx,
				"web_fetch: close connection after TLS handshake failure",
				conn.Close())
			return nil, fmt.Errorf("web_fetch: TLS handshake with %s: %w", host, err)
		}
		return tconn, nil
	}
}

// validatedAddresses resolves host (or returns the literal IP) and
// rejects any resolved address in private/loopback/link-local space.
// The caller dials exactly these addresses, so the check and the
// connection share one resolution.
func validatedAddresses(
	ctx context.Context, host string, allowPrivate bool,
) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !allowPrivate && blockedIP(ip) {
			return nil, errdefs.Validationf(
				"web_fetch: private address %q is blocked by the SSRF guard", host)
		}
		return []net.IP{ip}, nil
	}
	ips, err := lookupHost(ctx, host)
	if err != nil {
		return nil, errdefs.Validationf("web_fetch: resolve %q: %v", host, err)
	}
	if len(ips) == 0 {
		return nil, errdefs.Validationf("web_fetch: no addresses for %q", host)
	}
	if !allowPrivate {
		for _, ip := range ips {
			if blockedIP(ip) {
				return nil, errdefs.Validationf(
					"web_fetch: host %q resolves to private address %s (blocked by the SSRF guard)",
					host, ip)
			}
		}
	}
	return ips, nil
}

// dialIPs tries each validated address in order and returns the first
// successful connection.
func dialIPs(
	ctx context.Context,
	dialer *net.Dialer,
	network string,
	ips []net.IP,
	port string,
) (net.Conn, error) {
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(
			ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, fmt.Errorf("web_fetch: dial %s: %w", ips, lastErr)
	}
	return nil, errors.New("web_fetch: no reachable address")
}

// DomainGate builds the URL host gate from the configured policy:
// deny/allow host lists plus an SSRF guard that blocks
// private/loopback/link-local destinations unless AllowPrivate is set.
func DomainGate(pol sandbox.WebFetchPolicy) func(context.Context, string) error {
	return func(ctx context.Context, host string) error {
		h := strings.ToLower(strings.TrimSpace(host))
		if h == "" {
			return errdefs.Validationf("web_fetch: empty host")
		}
		if hostMatches(h, pol.DenyHosts) {
			return errdefs.Validationf(
				"web_fetch: host %q is denied by policy", host)
		}
		if len(pol.AllowHosts) > 0 && !hostMatches(h, pol.AllowHosts) {
			return errdefs.Validationf(
				"web_fetch: host %q is not in the allow list", host)
		}
		if !pol.AllowPrivate {
			if err := checkPublicHost(ctx, h); err != nil {
				return err
			}
		}
		return nil
	}
}

// YOLOBypassGate wraps a domain gate so YOLO sessions skip it
// entirely: the mode disables the sandbox by definition, so a web_fetch
// that would be unrestricted at the network layer should not be
// re-gated by the tool. Sessions without a persisted mode keep the
// inner gate.
func YOLOBypassGate(
	store *ocsessions.Store,
	gate func(context.Context, string) error,
) func(context.Context, string) error {
	return func(ctx context.Context, host string) error {
		if store != nil && sandbox.IsYOLO(ctx, store) {
			return nil
		}
		return gate(ctx, host)
	}
}

// hostMatches matches exact hosts and "*.example.com" suffix patterns.
func hostMatches(host string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if p == host {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(host, p[1:]) {
			return true
		}
	}
	return false
}

// checkPublicHost rejects IP literals and resolved destinations in
// private/loopback/link-local space (SSRF guard).
func checkPublicHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return errdefs.Validationf(
				"web_fetch: private address %q is blocked by the SSRF guard", host)
		}
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := lookupHost(dctx, host)
	if err != nil {
		return errdefs.Validationf("web_fetch: resolve %q: %v", host, err)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return errdefs.Validationf(
				"web_fetch: host %q resolves to private address %s (blocked by the SSRF guard)",
				host, ip)
		}
	}
	return nil
}

// lookupHost resolves a hostname to IPs for the SSRF guard. It is a
// variable so tests can stub DNS resolution and stay hermetic.
var lookupHost = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)
