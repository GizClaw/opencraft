// Package netguard builds the SSRF-hardened HTTP clients the tools use
// to reach destinations the model does not control: provider-issued
// artifact URLs and user-requested pages. Every connection resolves the
// host once, refuses private/loopback/link-local space unless the
// caller's policy allows it, and pins the dial to exactly the validated
// addresses, so neither a redirect hop nor a DNS rebind between the
// check and the dial can slip past the guard.
//
// It is deliberately policy-free: callers pass their own host gate and
// their own allow-private resolver, and the two tools that download
// (web_fetch, generate_video) share the machinery instead of keeping
// two security-critical copies of it.
package netguard

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"golang.org/x/net/http/httpproxy"
)

// Defaults for Options, and the resolution budget the pre-flight check
// gives one lookup.
const (
	defaultTimeout      = 30 * time.Second
	defaultMaxRedirects = 10
	resolveTimeout      = 5 * time.Second
)

// Options configures one hardened client.
type Options struct {
	// Label prefixes every error the client returns, e.g. "web_fetch";
	// it is the caller's name for the connection it asked for.
	Label string
	// Gate, when non-nil, is consulted with the hostname before every
	// connection attempt and every redirect hop, so host allow/deny
	// rules keep working even when the host is not a private literal.
	Gate func(ctx context.Context, host string) error
	// AllowPrivate, when non-nil, is consulted per request to decide
	// whether private/loopback/link-local destinations are permitted
	// (YOLO sessions and local providers opt in). Nil means always
	// blocked.
	AllowPrivate func(ctx context.Context) bool
	// Timeout bounds one whole request; zero means 30s.
	Timeout time.Duration
	// MaxRedirects bounds the redirect chain; zero means 10.
	MaxRedirects int
}

// Client builds an http.Client whose transport validates the
// destination host on every connection and whose redirect policy
// re-validates each hop.
func Client(opts Options) *http.Client {
	label := opts.Label
	if label == "" {
		label = "netguard"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		// Re-read the environment on every request (httpproxy, unlike
		// http.ProxyFromEnvironment, does not cache the first lookup), so
		// a caller-side proxy rejection and this transport agree on the
		// current proxy configuration.
		Proxy: func(req *http.Request) (*url.URL, error) {
			return EnvProxy(req.URL.String())
		},
		DialContext:           pinnedDial(label, opts.Gate, opts.AllowPrivate, dialer),
		DialTLSContext:        pinnedTLSDial(label, opts.Gate, opts.AllowPrivate, dialer),
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%s: stopped after %d redirects",
					label, maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%s: non-http(s) redirect %q",
					label, req.URL)
			}
			if opts.Gate != nil {
				if err := opts.Gate(req.Context(), req.URL.Hostname()); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// EnvProxy resolves the proxy configured for rawURL from the process
// environment. It is a variable so tests can stub it without mutating
// the process environment.
var EnvProxy = func(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return httpproxy.FromEnvironment().ProxyFunc()(u)
}

// LookupHost resolves a hostname to IPs for the SSRF guard. It is a
// variable so tests can answer without DNS.
var LookupHost = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// BlockedIP reports whether ip is in the space the guard refuses:
// loopback, private, link-local, or unspecified.
func BlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// ResolvePublic resolves host — a literal address or a name — and
// reports an error when any address it reaches is blocked. Callers use
// it for the pre-flight policy check; the dial path resolves again
// through the same seam and pins the connection to exactly those
// addresses.
func ResolvePublic(ctx context.Context, label, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if BlockedIP(ip) {
			return errdefs.Validationf(
				"%s: private address %q is blocked by the SSRF guard",
				label, host)
		}
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	ips, err := LookupHost(dctx, host)
	if err != nil {
		return errdefs.Validationf("%s: resolve %q: %v", label, host, err)
	}
	for _, ip := range ips {
		if BlockedIP(ip) {
			return errdefs.Validationf(
				"%s: host %q resolves to private address %s (blocked by the SSRF guard)",
				label, host, ip)
		}
	}
	return nil
}

// pinnedDial resolves addr once, validates the resolved addresses
// against the SSRF guard, and dials those exact addresses. The gate is
// consulted for the host lists as well, so allow/deny rules keep
// working even when the host is not a private literal.
func pinnedDial(
	label string,
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
	dialer *net.Dialer,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%s: parse dial address %q: %w", label, addr, err)
		}
		if gate != nil {
			if err := gate(ctx, host); err != nil {
				return nil, err
			}
		}
		ips, err := validatedAddresses(
			ctx, label, host, allowPrivate != nil && allowPrivate(ctx))
		if err != nil {
			return nil, err
		}
		return dialIPs(ctx, label, dialer, network, ips, port)
	}
}

// pinnedTLSDial is pinnedDial plus the TLS handshake against the
// hostname, so SNI/certificate validation still uses the original
// host while the connection itself targets the validated address.
func pinnedTLSDial(
	label string,
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
	dialer *net.Dialer,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%s: parse dial address %q: %w", label, addr, err)
		}
		if gate != nil {
			if err := gate(ctx, host); err != nil {
				return nil, err
			}
		}
		ips, err := validatedAddresses(
			ctx, label, host, allowPrivate != nil && allowPrivate(ctx))
		if err != nil {
			return nil, err
		}
		conn, err := dialIPs(ctx, label, dialer, network, ips, port)
		if err != nil {
			return nil, err
		}
		tconn := tls.Client(conn, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tconn.HandshakeContext(ctx); err != nil {
			telemetry.WarnErr(ctx,
				label+": close connection after TLS handshake failure",
				conn.Close())
			return nil, fmt.Errorf("%s: TLS handshake with %s: %w", label, host, err)
		}
		return tconn, nil
	}
}

// validatedAddresses resolves host (or returns the literal IP) and
// rejects any resolved address in private/loopback/link-local space.
// The caller dials exactly these addresses, so the check and the
// connection share one resolution.
func validatedAddresses(
	ctx context.Context, label, host string, allowPrivate bool,
) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !allowPrivate && BlockedIP(ip) {
			return nil, errdefs.Validationf(
				"%s: private address %q is blocked by the SSRF guard",
				label, host)
		}
		return []net.IP{ip}, nil
	}
	ips, err := LookupHost(ctx, host)
	if err != nil {
		return nil, errdefs.Validationf("%s: resolve %q: %v", label, host, err)
	}
	if len(ips) == 0 {
		return nil, errdefs.Validationf("%s: no addresses for %q", label, host)
	}
	if !allowPrivate {
		for _, ip := range ips {
			if BlockedIP(ip) {
				return nil, errdefs.Validationf(
					"%s: host %q resolves to private address %s (blocked by the SSRF guard)",
					label, host, ip)
			}
		}
	}
	return ips, nil
}

// dialIPs tries each validated address in order and returns the first
// successful connection.
func dialIPs(
	ctx context.Context,
	label string,
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
		return nil, fmt.Errorf("%s: dial %s: %w", label, ips, lastErr)
	}
	return nil, errors.New(label + ": no reachable address")
}
