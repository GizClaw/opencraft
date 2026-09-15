package desktop

import (
	"testing"

	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
)

func TestOTLPHeadersFromEnv(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{"empty", "", nil},
		{
			"single header",
			"x-honeycomb-team=abc123",
			map[string]string{"x-honeycomb-team": "abc123"},
		},
		{
			"two headers with padding",
			" authorization = Bearer t , x-tenant = a ",
			map[string]string{"authorization": "Bearer t", "x-tenant": "a"},
		},
		{
			// "+" is a literal in a credential: only percent escapes
			// are decoded, matching the OTel SDK's header parsing.
			"literal plus survives",
			"authorization=Bearer ab+cd/ef==",
			map[string]string{"authorization": "Bearer ab+cd/ef=="},
		},
		{
			"percent escapes decode",
			"authorization=Bearer%20a%2Cb",
			map[string]string{"authorization": "Bearer a,b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", tc.raw)
			got := otelHeadersFromEnv()
			if len(got) != len(tc.want) {
				t.Fatalf("headers = %v, want %v", got, tc.want)
			}
			for name, want := range tc.want {
				if got[name] != want {
					t.Fatalf("header %q = %q, want %q", name, got[name], want)
				}
			}
		})
	}
}

// The env headers feed the OTLP sink, so they must survive the sink
// validation the plugin path uses.
func TestOTLPEnvHeadersAreInstallable(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS",
		"authorization=Bearer%20ab%2Bcd, x-honeycomb-team=abc")
	sink := octelemetry.Sink{
		Endpoint: "collector.example:4318",
		Headers:  otelHeadersFromEnv(),
	}
	if err := sink.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
