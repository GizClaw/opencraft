package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
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

func updateInfoForTest(downloadURL, checksum string) plugins.UpdateInfo {
	return plugins.UpdateInfo{
		Version:     "0.2.0",
		DownloadURL: downloadURL,
		Checksum:    checksum,
	}
}
