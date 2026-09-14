package host

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/model"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestReasoningCapableThink(t *testing.T) {
	t.Run("reasoning model keeps the knob", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.InferenceConfig{Instances: []config.Instance{{
			StableID:  "a",
			Type:      "openai",
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models: []config.Model{{
				Name: "m1",
				Capabilities: model.ModelCapabilities{
					Reasoning: model.ReasoningCapability{
						Kind: model.ReasoningAlways,
					},
				},
			}},
		}}}
		if err := config.WriteInference(dir, cfg); err != nil {
			t.Fatal(err)
		}
		if got := reasoningCapableThink(dir, "", "medium"); got != "medium" {
			t.Fatalf("think = %q, want medium", got)
		}
	})

	t.Run("non-reasoning model drops the knob", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.InferenceConfig{Instances: []config.Instance{{
			StableID:  "a",
			Type:      "openai",
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models: []config.Model{{
				Name: "m0",
			}},
		}}}
		if err := config.WriteInference(dir, cfg); err != nil {
			t.Fatal(err)
		}
		if got := reasoningCapableThink(dir, "", "medium"); got != "" {
			t.Fatalf("think = %q, want empty", got)
		}
	})
}
