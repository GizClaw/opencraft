package httpprobe

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// installForTest turns the probe on and restores the process transport
// (and the once-only guard) when the test ends.
func installForTest(t *testing.T) {
	t.Helper()
	base := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = base
		installed.Store(false)
	})
	if !Install() {
		t.Fatal("Install reported the probe inactive")
	}
}

func bodyRecords(recorder *logcapture.Recorder, body string) int {
	count := 0
	for _, record := range recorder.Records() {
		if record.Body().AsString() == body {
			count++
		}
	}
	return count
}

// TestUninstalledWrapperStopsRecording pins the stand-down for clients
// that captured the wrapper. The provider SDKs hold the transport they
// were built with, so switching the probe off cannot rely on restoring
// http.DefaultTransport: a wrapper that was already handed out has to go
// quiet on its own.
func TestUninstalledWrapperStopsRecording(t *testing.T) {
	recorder := logcapture.Install(t)
	installForTest(t)
	// A client built while the probe was on keeps the wrapper.
	captured := &http.Client{Transport: http.DefaultTransport}

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	defer server.Close()
	post := func() {
		t.Helper()
		resp, err := captured.Post(server.URL+"/v1/responses",
			"application/json", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		resp.Body.Close()
	}

	post()
	if got := bodyRecords(recorder, "httpprobe: request dispatched"); got != 1 {
		t.Fatalf("dispatch records while installed = %d, want 1", got)
	}

	if !Uninstall() {
		t.Fatal("Uninstall reported nothing to remove")
	}
	post()
	if got := bodyRecords(recorder, "httpprobe: request dispatched"); got != 1 {
		t.Fatalf(
			"dispatch records after Uninstall = %d, want the captured wrapper to stay quiet",
			got)
	}
}

// TestUninstallRestoresProcessTransport pins the stand-down: a caller that
// cannot carry a wrapped transport (flowcraft's client builders, see the
// package comment) gets the process transport back, and Install puts the
// probe in place again afterwards.
func TestUninstallRestoresProcessTransport(t *testing.T) {
	base := http.DefaultTransport
	installForTest(t)
	if !Active() {
		t.Fatal("probe is not active after Install")
	}
	if http.DefaultTransport == base {
		t.Fatal("Install did not replace the process transport")
	}
	if !Uninstall() {
		t.Fatal("Uninstall reported nothing to remove")
	}
	if Active() {
		t.Fatal("probe is still active after Uninstall")
	}
	if http.DefaultTransport != base {
		t.Fatal("Uninstall did not restore the wrapped transport")
	}
	if Uninstall() {
		t.Fatal("Uninstall reported work on an uninstalled probe")
	}
	if !Install() {
		t.Fatal("Install did not put the probe back")
	}
}

// TestProbeSplitsProviderRoundTrip pins what the probe exists for: one
// inference-shaped POST produces a dispatched record (with the request
// size) and a headers record (with status and the wait), so a slow step
// can be split into local work and provider time.
func TestProbeSplitsProviderRoundTrip(t *testing.T) {
	recorder := logcapture.Install(t)
	installForTest(t)

	const delay = 30 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	payload := bytes.Repeat([]byte("x"), 4096)
	resp, err := http.Post(server.URL+"/v1/responses", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	if got := bodyRecords(recorder, "httpprobe: request dispatched"); got != 1 {
		t.Fatalf("dispatch records = %d, want 1", got)
	}
	if got := bodyRecords(recorder, "httpprobe: response headers received"); got != 1 {
		t.Fatalf("headers records = %d, want 1", got)
	}
	for _, record := range recorder.Records() {
		switch record.Body().AsString() {
		case "httpprobe: request dispatched":
			if got := logcapture.Attribute(record, "request_bytes"); got != "4096" {
				t.Fatalf("request_bytes = %q, want 4096", got)
			}
			if got := logcapture.Attribute(record, "http.path"); got != "/v1/responses" {
				t.Fatalf("http.path = %q, want /v1/responses", got)
			}
		case "httpprobe: response headers received":
			if got := logcapture.Attribute(record, "http.status"); got != "200" {
				t.Fatalf("http.status = %q, want 200", got)
			}
			// The wait is the number the split depends on, so it has to
			// cover the server's own delay rather than a rounding of it.
			if got := logcapture.Attribute(record, "wait_ms"); got == "" || got == "0" {
				t.Fatalf("wait_ms = %q, want the measured round trip", got)
			}
		}
	}
}

// TestProbeIgnoresReadsAndExports pins the two exclusions that keep the
// probe output to provider traffic: GETs, and the app's own OTLP export.
func TestProbeIgnoresReadsAndExports(t *testing.T) {
	recorder := logcapture.Install(t)
	installForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	resp, err = http.Post(server.URL+"/v1/logs", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("post export: %v", err)
	}
	resp.Body.Close()

	for _, body := range []string{
		"httpprobe: request dispatched",
		"httpprobe: response headers received",
	} {
		if got := bodyRecords(recorder, body); got != 0 {
			t.Fatalf("%s records = %d, want 0", body, got)
		}
	}
}
