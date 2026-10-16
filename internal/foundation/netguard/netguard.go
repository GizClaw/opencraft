// Package netguard builds the SSRF-hardened HTTP clients the tools use
// to reach destinations the model does not control: provider-issued
// artifact URLs and user-requested pages. Every connection resolves the
// host once, refuses private/loopback/link-local space unless the
// caller's policy allows it, and pins the dial to exactly the validated
// addresses, so neither a redirect hop nor a DNS rebind between the
// check and the dial can slip past the guard.
//
// A configured HTTP(S) proxy is refused while the guard is active: the
// pin cannot survive a proxy re-resolving the target. Callers whose
// policy allows private destinations have no pin to preserve and keep
// their proxy.
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
	// TLSConfig, when non-nil, is the base configuration for https
	// handshakes; the client clones it and forces ServerName to the
	// host it dialed (never the pinned address), so SNI and certificate
	// validation keep naming the original host. Tests use it to trust a
	// test server's CA.
	TLSConfig *tls.Config
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
		// HTTP/1.1 only, on purpose: Go keeps HTTP/2 off for a
		// transport with a custom dialer unless ForceAttemptHTTP2 asks
		// otherwise, and the pinned dialers below are the guard's only
		// connection path. Do not switch h2 on without teaching its
		// connection establishment the same pinning.
		//
		// The proxy is resolved per request (httpproxy, unlike
		// http.ProxyFromEnvironment, does not cache the first lookup)
		// and fails closed while the guard is active: through a proxy
		// the target would be re-resolved on the far side and the DNS
		// pin would be gone. RefuseProxy carries the same rule as a
		// caller-side pre-check.
		Proxy: func(req *http.Request) (*url.URL, error) {
			if err := RefuseProxy(label, req.URL.String(),
				opts.AllowPrivate != nil && opts.AllowPrivate(req.Context())); err != nil {
				return nil, err
			}
			return EnvProxy(req.URL.String())
		},
		DialContext:           pinnedDial(label, opts.Gate, opts.AllowPrivate, dialer),
		DialTLSContext:        pinnedTLSDial(label, opts.Gate, opts.AllowPrivate, dialer, opts.TLSConfig),
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

// RefuseProxy is the caller-side twin of the transport's proxy
// fail-closed rule: it reports the proxy configured for rawURL as an
// error when the request must be pinned (allowPrivate false) and a
// proxy would take over the resolution. netguard.Client applies the
// same rule to every request it makes, so this is only an earlier,
// caller-labeled refusal for callers that want the policy error before
// doing any work.
func RefuseProxy(label, rawURL string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	proxy, err := EnvProxy(rawURL)
	if err != nil {
		return errdefs.Validationf("%s: proxy resolution: %v", label, err)
	}
	if proxy != nil {
		return errdefs.Validationf(
			"%s: the SSRF guard cannot pin DNS through proxy %s; "+
				"unset HTTP(S)_PROXY/ALL_PROXY or enable allow_private",
			label, proxy.Redacted())
	}
	return nil
}

// LookupHost resolves a hostname to IPs for the SSRF guard. It is a
// variable so tests can answer without DNS.
var LookupHost = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// BlockedIP reports whether ip is in the space the guard refuses:
// loopback, private, link-local, multicast, unspecified, the IPv4
// special-purpose ranges the standard library does not classify
// (0.0.0.0/8, CGNAT, IETF protocol assignments, benchmarking,
// reserved), or an IPv6 encoding (NAT64, 6to4) that carries a blocked
// IPv4 address.
func BlockedIP(ip net.IP) bool {
	if blockedKind(ip) {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return blockedV4(v4)
	}
	if v4 := embeddedV4(ip); v4 != nil {
		return blockedKind(v4) || blockedV4(v4)
	}
	return false
}

// blockedKind is the address-class check shared by IPv4 and IPv6.
func blockedKind(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// blockedV4 covers the IPv4-only ranges IsPrivate does not:
// 0.0.0.0/8 (RFC 1122 "this host on this network"; Linux routes it to
// loopback, so reaching 0.x is reaching this host), 100.64.0.0/10
// (carrier-grade NAT shared space, where a service behind the carrier's
// NAT can sit), 192.0.0.0/24 (IETF protocol assignments), 198.18.0.0/15
// (benchmarking) and 240.0.0.0/4 (reserved, including the broadcast
// address).
func blockedV4(v4 net.IP) bool {
	if len(v4) != 4 {
		return false
	}
	switch {
	case v4[0] == 0:
		return true
	case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
		return true
	case v4[0] == 192 && v4[1] == 0 && v4[2] == 0:
		return true
	case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19):
		return true
	case v4[0] >= 240:
		return true
	}
	return false
}

// embeddedV4 decodes the IPv4 address carried inside the two IPv6
// encodings that re-encode one: 6to4 2002::/16 and NAT64 64:ff9b::/96.
// Without this a private IPv4 target could reach the guard spelled as a
// "public" IPv6 one. The encodings themselves stay usable while the
// embedded address is public.
func embeddedV4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len {
		return nil
	}
	// 2002::/16: 6to4 embeds the v4 address in bytes 2..5.
	if ip[0] == 0x20 && ip[1] == 0x02 {
		return net.IP(ip[2:6])
	}
	// 64:ff9b::/96: NAT64 embeds the v4 address in the last 4 bytes.
	for i, b := range []byte{0x00, 0x64, 0xff, 0x9b} {
		if ip[i] != b {
			return nil
		}
	}
	for i := 4; i < 12; i++ {
		if ip[i] != 0 {
			return nil
		}
	}
	return net.IP(ip[12:16])
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
// base, when non-nil, is cloned into the handshake config (a test
// injects its trust anchors this way); ServerName is always forced to
// the dialed host, so no injected config can redirect validation.
func pinnedTLSDial(
	label string,
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
	dialer *net.Dialer,
	base *tls.Config,
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
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if base != nil {
			cfg = base.Clone()
		}
		cfg.ServerName = host
		tconn := tls.Client(conn, cfg)
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
