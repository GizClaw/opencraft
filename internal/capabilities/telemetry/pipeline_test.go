package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coretelemetry "github.com/GizClaw/flowcraft/core/telemetry"
)

func TestSinkValidate(t *testing.T) {
	longValue := strings.Repeat("v", maxSinkHeaderValue+1)
	manyHeaders := map[string]string{}
	for i := 0; i <= maxSinkHeaders; i++ {
		manyHeaders["X-Header-"+strings.Repeat("a", i+1)] = "v"
	}
	cases := []struct {
		name string
		sink Sink
		want string
	}{
		{"missing endpoint", Sink{}, "endpoint is required"},
		{"credentials in endpoint", Sink{
			Endpoint: "user:pass@collector.example:4318"}, "credentials in headers"},
		{"unsupported scheme", Sink{
			Endpoint: "ftp://collector.example:4318"}, "not supported"},
		{"plaintext remote", Sink{
			Endpoint: "http://collector.example:4318"}, "only allowed for loopback"},
		{"endpoint path", Sink{
			Endpoint: "collector.example:4318/v1/logs"}, "must not carry a path"},
		{"endpoint query", Sink{
			Endpoint: "collector.example:4318?tenant=a"}, "must not carry a query"},
		{"reserved header", Sink{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"Host": "evil.example"}}, "reserved"},
		{"invalid header name", Sink{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"X Bad": "v"}}, "not a valid token"},
		{"newline in header", Sink{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"X-Key": "a\nb"}}, "must not contain CR or LF"},
		{"oversized header", Sink{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"X-Key": longValue}}, "exceeds"},
		{"too many headers", Sink{
			Endpoint: "collector.example:4318",
			Headers:  manyHeaders}, "limit is"},
		{"tls collector", Sink{Endpoint: "collector.example:4318"}, ""},
		{"tls scheme", Sink{Endpoint: "https://collector.example:4318"}, ""},
		{"loopback plaintext", Sink{Endpoint: "http://127.0.0.1:4318"}, ""},
		{"auth header", Sink{
			Endpoint: "collector.example:4318",
			Headers:  map[string]string{"Authorization": "Bearer t"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.sink.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.want != "" && err == nil:
				t.Fatalf("Validate() = nil, want error containing %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

// otlpCollector is a minimal OTLP/HTTP receiver: it accepts every
// signal and records the auth header of log exports.
type otlpCollector struct {
	server *httptest.Server

	mu     sync.Mutex
	byAuth map[string]int
}

func newOTLPCollector(t *testing.T) *otlpCollector {
	t.Helper()
	c := &otlpCollector{byAuth: map[string]int{}}
	c.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/logs") {
				c.mu.Lock()
				c.byAuth[r.Header.Get("Authorization")]++
				c.mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
		}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *otlpCollector) endpoint() string {
	return strings.TrimPrefix(c.server.URL, "http://")
}

func (c *otlpCollector) logsWith(auth string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byAuth[auth]
}

func TestPipelineSwapsSinkAndKeepsFileSink(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "opencraft.log")
	ctx := context.Background()
	first := newOTLPCollector(t)
	second := newOTLPCollector(t)

	p, err := Start(ctx, TelemetryOptions{LogFile: logPath})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if sink, owner := p.Sink(); sink.Endpoint != "" || owner != "" {
		t.Fatalf("base sink = %+v owner %q, want application-owned empty sink",
			sink, owner)
	}

	coretelemetry.Info(ctx, "record-before-swap")
	sinkOne := Sink{
		Endpoint: first.endpoint(),
		Headers:  map[string]string{"Authorization": "Bearer first"},
	}
	if err := p.Reconfigure(ctx, sinkOne, "plug-a"); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	sink, owner := p.Sink()
	if owner != "plug-a" || sink.Endpoint != first.endpoint() || !sink.Insecure {
		t.Fatalf("active sink = %+v owner %q", sink, owner)
	}
	if got := sink.Headers["Authorization"]; got != "Bearer first" {
		t.Fatalf("active header = %q", got)
	}

	// A rejected sink must leave the live pipeline untouched.
	if err := p.Reconfigure(ctx,
		Sink{Endpoint: "user:pass@collector.example:4318"}, "plug-c",
	); err == nil {
		t.Fatal("expected the credential-bearing endpoint to be rejected")
	}
	if sink, owner := p.Sink(); owner != "plug-a" || sink.Endpoint != first.endpoint() {
		t.Fatalf("rejected reconfigure changed the sink: %+v owner %q", sink, owner)
	}

	coretelemetry.Info(ctx, "record-first-swap")
	// Taking the sink over drains the previous pipeline, which is what
	// flushes the record above to the first collector.
	if err := p.Reconfigure(ctx,
		Sink{Endpoint: second.endpoint(), Headers: map[string]string{
			"Authorization": "Bearer second",
		}}, "plug-b",
	); err != nil {
		t.Fatalf("reconfigure second: %v", err)
	}
	coretelemetry.Info(ctx, "record-second-swap")
	if err := p.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if sink, owner := p.Sink(); owner != "" || sink.Endpoint != "" {
		t.Fatalf("after reset = %+v owner %q, want application-owned empty sink",
			sink, owner)
	}
	if got := first.logsWith("Bearer first"); got == 0 {
		t.Fatal("first collector saw no log export with its auth header")
	}
	if got := second.logsWith("Bearer second"); got == 0 {
		t.Fatal("second collector saw no log export with its auth header")
	}

	// The local file sink survives every swap.
	waitForLogFile(t, logPath, []string{
		"record-before-swap", "record-first-swap", "record-second-swap",
	})
}

func waitForLogFile(t *testing.T, path string, wants []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var out string
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			out = string(b)
			missing := false
			for _, want := range wants {
				if !strings.Contains(out, want) {
					missing = true
					break
				}
			}
			if !missing {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("log file missing records %v:\n%s", wants, out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
