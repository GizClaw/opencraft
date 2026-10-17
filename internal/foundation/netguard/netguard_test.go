package netguard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestBlockedIP pins the blocked-address classes: the IPv6 encodings
// that carry an embedded IPv4 (NAT64, 6to4), the IPv4 ranges the
// standard library does not call private, and the IPv6-only
// special-purpose prefixes (documentation, Teredo, the local-use
// translator prefix). Every mask sits next to a sample just outside it,
// so widening one is a red test rather than a silent hole.
func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.255.255.255", "::1",
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fd00::1",
		"169.254.1.1", "fe80::1", "ff02::1",
		"0.0.0.0", "0.1.2.3", "::",
		"100.64.0.1", "100.127.255.255",
		"192.0.0.9", "198.18.0.1", "198.19.255.255", "240.0.0.1", "255.255.255.255",
		"224.0.0.1", "239.255.255.250", "ff0e::1",
		"192.0.2.1", "192.88.99.1", "198.51.100.7", "203.0.113.9",
		"::ffff:10.0.0.1", "::ffff:0.0.0.1",
		"::ffff:239.255.255.250",
		"64:ff9b::10.0.0.1", "64:ff9b::127.0.0.1", "64:ff9b::169.254.1.1",
		"2002:0a00:0001::", "2002:7f00:0001::",
		"2001:db8::1", "3fff::1", "2001::1",
		// 64:ff9b:1::/48 is refused whole: a public v4 spelled inside
		// it does not make the container reachable.
		"64:ff9b:1::a00:1", "64:ff9b:1::808:808",
		"64:ff9b:1:abcd:0:5431:c000:221",
	}
	for _, raw := range blocked {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("bad fixture %q", raw)
		}
		if !BlockedIP(ip) {
			t.Errorf("BlockedIP(%s) = false, want true", raw)
		}
	}

	allowed := []string{
		"8.8.8.8", "1.1.1.1", "172.32.0.1", "100.63.0.1", "100.128.0.1",
		"192.0.1.1", "192.0.3.1", "198.17.0.1", "198.20.0.1",
		"198.51.101.1", "203.0.114.1",
		"2606:4700:4700::1111",
		"2001:4860:4860::8888",
		"3fff:1000::1",
		"2001:db9::1", "2001:1::1", // outside the documentation and Teredo blocks
		"64:ff9b::8.8.8.8",                // NAT64 is fine while the embedded v4 is public.
		"2002:0808:0808::",                // so is 6to4.
		"64:ff9a:ffff::1", "64:ff9b:2::1", // the local-use /48's neighbours
	}
	for _, raw := range allowed {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("bad fixture %q", raw)
		}
		if BlockedIP(ip) {
			t.Errorf("BlockedIP(%s) = true, want false", raw)
		}
	}
}

// TestClientPinsTLSAndValidatesCertificates exercises the https dial
// path end to end: the handshake must complete against the test
// server's certificate (proving the pinned TLS dial works, trusting the
// injected anchor) and must fail without the anchor (proving the
// handshake still validates the certificate).
func TestClientPinsTLSAndValidatesCertificates(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(
		w http.ResponseWriter, _ *http.Request,
	) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	trusting := Client(Options{
		Label:        "test",
		AllowPrivate: func(context.Context) bool { return true },
		TLSConfig:    &tls.Config{RootCAs: pool},
	})
	resp, err := trusting.Get(srv.URL)
	if err != nil {
		t.Fatalf("tls get: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close body: %v", cerr)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "ok" {
		t.Fatalf("body = %q, %v; want ok", body, err)
	}

	// The same dial without the trust anchor must fail the handshake.
	strict := Client(Options{
		Label:        "test",
		AllowPrivate: func(context.Context) bool { return true },
	})
	if _, err := strict.Get(srv.URL); err == nil {
		t.Fatal("https to the test server succeeded without trusting its CA")
	}
}

// TestClientBlocksPrivateAndFailsClosedUnderProxy pins the two
// transport-level refusals: a private destination without the
// allow-private policy, and a guarded request while a proxy is
// configured (a proxy re-resolves the target, so the DNS pin cannot
// survive it).
func TestClientBlocksPrivateAndFailsClosedUnderProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, _ *http.Request,
	) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	guarded := Client(Options{Label: "test"})
	if _, err := guarded.Get(srv.URL); err == nil ||
		!strings.Contains(err.Error(), "blocked by the SSRF guard") {
		t.Fatalf("guarded get to loopback = %v, want SSRF rejection", err)
	}

	orig := EnvProxy
	EnvProxy = func(string) (*url.URL, error) {
		return url.Parse("http://127.0.0.1:9")
	}
	t.Cleanup(func() { EnvProxy = orig })

	if _, err := guarded.Get("http://public.example.test/"); err == nil ||
		!strings.Contains(err.Error(), "cannot pin DNS through proxy") {
		t.Fatalf("guarded get through a proxy = %v, want proxy rejection", err)
	}

	// allow-private keeps proxies usable: the guard is what would have
	// been bypassed, and this caller waived it.
	open := Client(Options{
		Label:        "test",
		AllowPrivate: func(context.Context) bool { return true },
	})
	if _, err := open.Get("http://public.example.test/"); err != nil &&
		strings.Contains(err.Error(), "cannot pin DNS through proxy") {
		t.Fatalf("allow-private client refused its proxy: %v", err)
	}
}
