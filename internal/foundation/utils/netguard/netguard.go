// Package netguard holds the rules for the fetches the host performs on
// its own initiative — as opposed to the ones a tool call drives, whose
// policy lives with the tool (webfetch, the sandbox's netpolicy).
//
// Its callers fetch a URL that arrived from somewhere other than the
// user's keyboard: a plugin manifest's update.url, an application
// market source. Both therefore fetch on the strict side of the rules:
// an absolute http(s) URL, no credentials, no fragment, a host that
// resolves to a public address, a bounded redirect chain, and a timeout
// that keeps a silent server from holding the host open. The zero
// Policy enforces all of it; AllowPrivate is the escape for tests and
// development hosts, named for exactly what it permits.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// MaxRedirects bounds one fetch's redirect chain.
	MaxRedirects = 5
	// RequestTimeout bounds one whole request.
	RequestTimeout = 30 * time.Second
	// DialTimeout bounds one connection attempt — and the host lookup
	// that precedes it.
	DialTimeout = 10 * time.Second
)

// Policy controls the network restrictions of one fetch.
type Policy struct {
	// AllowPrivate permits loopback, private, link-local and plain
	// http destinations. The zero value is the shipped one: only
	// public https.
	AllowPrivate bool
}

// CheckURL validates a URL under pol: the scheme, the shape, and where
// its host resolves.
func CheckURL(ctx context.Context, u *url.URL, pol Policy) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if !pol.AllowPrivate && u.Scheme != "https" {
		return errors.New("http is only allowed for private or test hosts")
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("url must be absolute, without credentials or fragment")
	}
	return CheckHost(ctx, u.Hostname(), pol)
}

// CheckHost refuses a host that resolves anywhere but the public
// internet. An IP literal is judged without a lookup; under
// AllowPrivate neither the lookup nor the judgement happens, because an
// address the policy allowed is not one to resolve behind the caller's
// back.
func CheckHost(ctx context.Context, host string, pol Policy) error {
	if ip := net.ParseIP(host); ip != nil {
		if !pol.AllowPrivate && BlockedIP(ip) {
			return fmt.Errorf("private address %q is blocked", host)
		}
		return nil
	}
	if pol.AllowPrivate {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(dctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if BlockedIP(ip) {
			return fmt.Errorf(
				"host %q resolves to private address %s (blocked)", host, ip)
		}
	}
	return nil
}

// BlockedIP reports whether one address is one the host will not fetch
// from: loopback, private, link-local or unspecified.
func BlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// Client returns the HTTP client every guarded fetch uses: a dial-time
// host check (so an address that changes between the check and the
// connect — or that a redirect introduces — still has to pass), the
// request timeout, and a redirect cap that re-validates each hop.
func Client(pol Policy) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(
			ctx context.Context,
			network, addr string,
		) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if err := CheckHost(ctx, host, pol); err != nil {
				return nil, err
			}
			dialer := &net.Dialer{Timeout: DialTimeout}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   RequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return errors.New("too many redirects")
			}
			return CheckURL(req.Context(), req.URL, pol)
		},
	}
}
