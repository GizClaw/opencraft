package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestClientRevalidatesEveryRedirect: a hop is judged as a URL of its
// own, not only as a socket to open. The dial-time check reads the host,
// so it can see a redirect into a private address; what it cannot see is
// everything else the rules are about — here, a Location that carries a
// user and a password, which is a server handing the next host a
// credential the user never typed. The hop is refused before the request
// for it exists, so the server that sent the redirect sees one request
// and not two.
func TestClientRevalidatesEveryRedirect(t *testing.T) {
	var ts *httptest.Server
	hits := 0
	ts = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			hits++
			target := "http://user:secret@" +
				strings.TrimPrefix(ts.URL, "http://") + "/next"
			http.Redirect(w, r, target, http.StatusFound)
		}))
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, ts.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Client(Policy{AllowPrivate: true}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a redirect carrying credentials was followed")
	}
	if !strings.Contains(err.Error(), "without credentials or fragment") {
		t.Fatalf("err = %v, want the redirect refused as a URL", err)
	}
	if hits != 1 {
		t.Errorf("the server saw %d requests, want the redirect refused before one was made", hits)
	}
}

// TestClientCountsTheRedirectCapToTheHop: the cap is a number of hops,
// and the fetch that stays under it succeeds. Both halves matter — a
// client that refused one hop early would break ordinary URLs (a
// canonical-route redirect, an http→https upgrade), which is the failure
// a test that only watches for "too many redirects" cannot see.
func TestClientCountsTheRedirectCapToTheHop(t *testing.T) {
	// hopChain serves 200 once it has sent `redirects` redirects, so a
	// successful fetch is what says the chain was followed to the end.
	hopChain := func(redirects int) (*httptest.Server, *int) {
		seen := 0
		var ts *httptest.Server
		ts = httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				seen++
				if seen <= redirects {
					http.Redirect(w, r, ts.URL+"/next", http.StatusFound)
					return
				}
				_, _ = w.Write([]byte("done"))
			}))
		return ts, &seen
	}

	// One hop short of the cap: followed.
	ts, seen := hopChain(MaxRedirects - 1)
	defer ts.Close()
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, ts.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := Client(Policy{AllowPrivate: true})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("a chain of %d redirects failed: %v", MaxRedirects-1, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %s, want 200", resp.Status)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if *seen != MaxRedirects {
		t.Errorf("the server saw %d requests, want %d", *seen, MaxRedirects)
	}

	// And at the cap: refused.
	ts2, seen2 := hopChain(MaxRedirects)
	defer ts2.Close()
	req2, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, ts2.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req2); err == nil ||
		!strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("a chain of %d redirects = %v, want too many redirects", MaxRedirects, err)
	}
	if *seen2 != MaxRedirects {
		t.Errorf("the server saw %d requests, want the last redirect refused", *seen2)
	}
}

// TestCheckURLJudgesTheShapeAndTheHost: what the host will fetch is a
// URL the user could not have typed into a tool call, so the rules are
// read off the string itself — scheme, credentials, fragment — and off
// the host it names. A public address literal keeps the test off the
// network; the private ones are refused without one.
func TestCheckURLJudgesTheShapeAndTheHost(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		raw     string
		pol     Policy
		wantErr string
	}{
		{name: "https to a public address",
			raw: "https://93.184.216.34/pkg.zip"},
		{name: "plain http without the policy",
			raw:     "http://93.184.216.34/pkg.zip",
			wantErr: "http is only allowed for private or test hosts"},
		{name: "plain http with the policy",
			raw: "http://127.0.0.1:8080/pkg.zip",
			pol: Policy{AllowPrivate: true}},
		{name: "another scheme",
			raw:     "ftp://93.184.216.34/pkg.zip",
			wantErr: `unsupported scheme "ftp"`},
		{name: "credentials travel in the URL",
			raw:     "https://user:secret@93.184.216.34/pkg.zip",
			wantErr: "without credentials or fragment"},
		{name: "a fragment is not sent to the server",
			raw:     "https://93.184.216.34/pkg.zip#main",
			wantErr: "without credentials or fragment"},
		{name: "no host at all",
			raw:     "https:///pkg.zip",
			wantErr: "without credentials or fragment"},
		{name: "a loopback literal",
			raw:     "https://127.0.0.1/pkg.zip",
			wantErr: `private address "127.0.0.1" is blocked`},
		{name: "a private literal",
			raw:     "https://10.1.2.3/pkg.zip",
			wantErr: `private address "10.1.2.3" is blocked`},
		{name: "a name that resolves privately",
			raw:     "https://localhost/pkg.zip",
			wantErr: `resolves to private address`},
		{name: "a private name the policy allows",
			raw: "https://localhost/pkg.zip",
			pol: Policy{AllowPrivate: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.raw, err)
			}
			err = CheckURL(ctx, u, tc.pol)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("CheckURL(%q) = %v, want it accepted", tc.raw, err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("CheckURL(%q) accepted, want %q", tc.raw, tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("CheckURL(%q) = %v, want it to read %q", tc.raw, err, tc.wantErr)
			}
		})
	}
}

// TestBlockedIPWorksTheAddressClasses: the list is the whole rule, and
// it is a list rather than a prefix comparison because "10.x is private
// but 100.64.x is not" is not something to re-derive at a call site.
func TestBlockedIPWorksTheAddressClasses(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.1.2.3", "192.168.1.1", "172.16.5.5",
		"169.254.10.10", "fe80::1", "0.0.0.0", "::",
	}
	for _, raw := range blocked {
		if ip := net.ParseIP(raw); ip == nil || !BlockedIP(ip) {
			t.Errorf("BlockedIP(%s) = false, want blocked", raw)
		}
	}
	allowed := []string{"93.184.216.34", "2606:4700:4700::1111", "100.64.0.1"}
	for _, raw := range allowed {
		if ip := net.ParseIP(raw); ip == nil || BlockedIP(ip) {
			t.Errorf("BlockedIP(%s) = true, want allowed", raw)
		}
	}
}

// TestClientRefusesADialTheURLCheckWouldHavePassed: the dial-time check
// is the one that closes the gap between "the URL says one host" and
// "the connection goes to another" — a redirect, a proxy or an answer
// that changed between the lookup and the connect. A test server lives
// on loopback, so it is exactly the destination the default policy must
// refuse even when the request is otherwise well formed.
func TestClientRefusesADialTheURLCheckWouldHavePassed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Client(Policy{}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("a loopback dial succeeded under the default policy")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("dial error = %v, want it to say the address is blocked", err)
	}

	allowed, err := Client(Policy{AllowPrivate: true}).Do(req)
	if err != nil {
		t.Fatalf("the policy that permits private destinations refused one: %v", err)
	}
	defer func() {
		if err := allowed.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()
	if allowed.StatusCode != http.StatusOK {
		t.Errorf("status = %s, want 200", allowed.Status)
	}
}

// TestClientCapsRedirects: a fetch that keeps being redirected is a
// fetch that never ends, and each hop re-reads the rules.
func TestClientCapsRedirects(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, ts.URL+"/again", http.StatusFound)
		}))
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Client(Policy{AllowPrivate: true}).Do(req)
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("err = %v, want too many redirects", err)
	}
}
