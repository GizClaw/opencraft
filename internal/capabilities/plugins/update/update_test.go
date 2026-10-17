package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/foundation/netguard"
)

func TestCheckAndFetchZip(t *testing.T) {
	zipBytes := []byte("plugin-zip-content")
	sum := sha256.Sum256(zipBytes)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"version":"0.2.0","download_url":"%s/plugin.zip","checksum":%q,"changelog":"fixes"}`,
			"http://"+r.Host, checksum)
	})
	mux.HandleFunc("/plugin.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	pol := Policy{AllowPrivate: true}
	info, err := CheckWithPolicy(
		context.Background(), ts.URL+"/latest.json", pol)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Version != "0.2.0" || info.Checksum != checksum {
		t.Fatalf("info = %+v", info)
	}
	path, cleanup, err := FetchZip(context.Background(), info, pol)
	if err != nil {
		t.Fatalf("FetchZip: %v", err)
	}
	defer cleanup()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(zipBytes) {
		t.Fatalf("zip content = %q", data)
	}
}

func TestCheckRejectsPrivateWithoutPolicy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.2.0","download_url":"https://example.com/p.zip","checksum":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if _, err := CheckWithPolicy(
		context.Background(), ts.URL+"/latest.json", Policy{},
	); err == nil {
		t.Fatal("private/http update source must be rejected by the default policy")
	}
}

func TestCheckRejectsInvalidVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"abc","download_url":"https://example.com/p.zip","checksum":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if _, err := CheckWithPolicy(
		context.Background(), ts.URL+"/latest.json", Policy{AllowPrivate: true},
	); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("Check error = %v, want invalid version", err)
	}
}

func TestFetchZipChecksumMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/p.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	info := updateInfoForTest(ts.URL+"/p.zip",
		"sha256:0000000000000000000000000000000000000000000000000000000000000000")
	pol := Policy{AllowPrivate: true}
	if _, _, err := FetchZip(context.Background(), info, pol); err == nil ||
		!strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("FetchZip error = %v, want checksum mismatch", err)
	}
}

// TestProxyIsRefused pins the guard's proxy rule on this path: the
// address checks can only speak for a connection this process makes
// itself, so a proxy in the environment — which would resolve the target
// on the far side — is refused before a request goes out. Both entry
// points are read here because each builds its own client, and
// AllowPrivate releases the refusal the way it releases every other
// check in this package.
func TestProxyIsRefused(t *testing.T) {
	old := netguard.EnvProxy
	netguard.EnvProxy = func(string) (*url.URL, error) {
		return url.Parse("http://proxy.example:3128")
	}
	defer func() { netguard.EnvProxy = old }()

	// An address literal, so the check that runs first does not depend
	// on DNS: the host check passes and the transport's proxy func is
	// what refuses.
	const manifestURL = "https://1.1.1.1/latest.json"
	_, err := CheckWithPolicy(context.Background(), manifestURL, Policy{})
	if err == nil || !strings.Contains(err.Error(), "cannot pin DNS") {
		t.Fatalf("CheckWithPolicy error = %v, want the proxy refusal", err)
	}

	info := plugins.UpdateInfo{
		Version:     "0.2.0",
		DownloadURL: "https://1.1.1.1/plugin.zip",
		Checksum:    "sha256:" + strings.Repeat("a", 64),
	}
	if _, _, err := FetchZip(context.Background(), info, Policy{}); err == nil ||
		!strings.Contains(err.Error(), "cannot pin DNS") {
		t.Fatalf("FetchZip error = %v, want the proxy refusal", err)
	}

	// The private-destination policy keeps its proxy: the pin this rule
	// protects is not in play when the destination is allowed anyway.
	if err := netguard.RefuseProxy("plugin update", manifestURL, true); err != nil {
		t.Fatalf("allow-private policy should keep its proxy: %v", err)
	}
}

func updateInfoForTest(downloadURL, checksum string) plugins.UpdateInfo {
	return plugins.UpdateInfo{
		Version:     "0.2.0",
		DownloadURL: downloadURL,
		Checksum:    checksum,
	}
}
