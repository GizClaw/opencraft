package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
)

// goldenTime pins the document header so the golden file is stable.
var goldenTime = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// goldenFixture covers every branch the writer has: the OpenAI wire with
// its full advanced block, ByteDance's flat transport fields and video
// model facts, MiniMax's media origin, a disabled instance, per-model
// endpoints, lifecycle, driver fields, three key sources, and the router
// retry policy.
func goldenFixture() InferenceConfig {
	maxInput, maxOutput, retries := 1_000_000, 384_000, 3
	return InferenceConfig{
		Router: RouterPolicy{MaxAttempts: 4, FallbackOnRetryExhausted: false},
		Instances: []Instance{
			{
				StableID:  "inst-aaa",
				Type:      "openai",
				Name:      "gateway",
				API:       "chat",
				Endpoint:  "https://gateway.example/v1",
				KeySource: KeyEnv,
				Enabled:   true,
				Advanced: InstanceAdvanced{
					Routing:                "azure_deployment",
					Query:                  map[string]string{"api-version": "2025-04-01-preview"},
					Headers:                map[string]string{"x-gateway": "one"},
					Organization:           "org-1",
					Project:                "proj-2",
					Timeout:                "90s",
					AuthScheme:             "header",
					AuthHeader:             "x-api-key",
					MetadataEnvelope:       "-",
					HTTPRetries:            &retries,
					Store:                  "omit",
					ExtraBody:              map[string]string{"custom_hint": `"prefer-a"`},
					ReasoningChannel:       "text",
					ReasoningSummary:       "auto",
					ReasoningScope:         "gateway-2026",
					Truncation:             "auto",
					VideoInput:             true,
					ChatIncludeUsage:       boolPtr(false),
					ChatIncludeObfuscation: boolPtr(true),
				},
				Models: []Model{
					{
						Name: "glm-5.3-flash",
						Capabilities: model.ModelCapabilities{
							Inputs: []message.PartKind{
								message.PartText, message.PartImage,
							},
							Outputs:         []message.PartKind{message.PartText},
							HostedWebSearch: true,
							Reasoning: model.ReasoningCapability{
								Kind: model.ReasoningToggle,
								EffortMap: map[model.ReasoningEffort]string{
									model.ReasoningMinimal: "low",
									model.ReasoningLow:     "low",
									model.ReasoningMedium:  "high",
									model.ReasoningHigh:    "high",
									model.ReasoningXHigh:   "max",
								},
							},
						},
						Limits: model.ModelLimits{
							MaxInputTokens:  &maxInput,
							MaxOutputTokens: &maxOutput,
						},
						Lifecycle: ModelLifecycle{
							Status:              "deprecated",
							ReplacementProvider: "openai",
							ReplacementName:     "gpt-5.6-terra",
							Notes:               "sunset later this year",
						},
					},
					{Name: "glm-5.3-air"},
				},
			},
			{
				StableID:  "inst-bbb",
				Type:      "bytedance",
				Name:      "ark",
				KeySource: KeyKeychain,
				KeyValue:  "inference/ark-account",
				Endpoint:  "https://ark.cn-beijing.volces.com/api/v3",
				Enabled:   false,
				Advanced: InstanceAdvanced{
					Region:                  "cn-beijing",
					Project:                 "proj-ark",
					Timeout:                 "2m",
					Headers:                 map[string]string{"x-ark": "one"},
					Query:                   map[string]string{"version": "2024-01-01"},
					VideoPollIntervalMillis: intPtr(5000),
				},
				Models: []Model{{
					Name:     "doubao-seedance-2-0",
					Kind:     "video",
					Endpoint: "ep-2026-ark",
					Capabilities: model.ModelCapabilities{
						Outputs: []message.PartKind{message.PartVideo},
					},
					DriverFields: map[string]any{
						"max_resolution": "1080p",
						"video": map[string]any{
							"seed":                 true,
							"duration_min_seconds": float64(5),
						},
					},
				}},
			},
			{
				StableID:  "inst-ccc",
				Type:      "minimax",
				KeySource: KeyLiteral,
				KeyValue:  "sk-it's-secret",
				Enabled:   true,
				Advanced: InstanceAdvanced{
					MediaBaseURL: "https://api.minimax.io",
				},
				Models: []Model{{
					Name: "image-01",
					Kind: "image",
					Capabilities: model.ModelCapabilities{
						Outputs: []message.PartKind{message.PartImage},
					},
				}},
			},
		},
	}
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

// TestInferenceYAMLGolden pins the written document byte for byte. Run
// with UPDATE_GOLDEN=1 to regenerate after an intentional change.
func TestInferenceYAMLGolden(t *testing.T) {
	got, err := goldenFixture().inferenceYAMLAt(goldenTime)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	path := filepath.Join("testdata", "inference.golden.yaml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("written document changed:\n--- want\n%s\n--- got\n%s", want, got)
	}
}
