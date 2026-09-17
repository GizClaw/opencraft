package config

import (
	"strings"
	"testing"
)

// TestLowerRejectsAPIModeOutsideOpenAIWire pins the row contract: the
// responses | chat surface belongs to the OpenAI wire family, and a
// driver without it must say so instead of dropping the choice.
func TestLowerRejectsAPIModeOutsideOpenAIWire(t *testing.T) {
	enabled := true
	for _, tc := range []struct {
		name string
		spec InstanceSpec
		ok   bool
	}{
		{
			name: "openai chat",
			spec: InstanceSpec{
				Type: "openai", API: "chat", KeySource: KeySourceLiteralName, KeyValue: "k",
				Enabled: &enabled, Models: []ModelSpec{{Name: "m"}},
			},
			ok: true,
		},
		{
			name: "plugin vendor on the openai wire",
			spec: InstanceSpec{
				Type: "acme-gateway", Driver: "openai", API: "responses",
				KeySource: KeySourceLiteralName, KeyValue: "k", Enabled: &enabled,
				Models: []ModelSpec{{Name: "m"}},
			},
			ok: true,
		},
		{
			name: "bytedance",
			spec: InstanceSpec{
				Type: "bytedance", API: "chat", KeySource: KeySourceLiteralName, KeyValue: "k",
				Enabled: &enabled, Models: []ModelSpec{{Name: "m"}},
			},
		},
		{
			name: "minimax",
			spec: InstanceSpec{
				Type: "minimax", API: "responses", KeySource: KeySourceLiteralName, KeyValue: "k",
				Enabled: &enabled, Models: []ModelSpec{{Name: "m"}},
			},
		},
		{
			name: "anthropic",
			spec: InstanceSpec{
				Type: "anthropic", API: "chat", KeySource: KeySourceLiteralName, KeyValue: "k",
				Enabled: &enabled, Models: []ModelSpec{{Name: "m"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.spec.Lower(SourceUser, "")
			if tc.ok {
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "does not take an api mode") {
				t.Fatalf("error = %v, want an api-mode rejection", err)
			}
		})
	}
}
