package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// TestBuildRuntimeAcceptsDriverFacts pins the shapes the settings page
// writes for the declaration leaves flowcraft's drivers added: model
// lifecycle, the unmodeled body-field bag, the retention policy's
// "omit", and ByteDance's per-model video facts. A runtime that builds
// means the released drivers decoded all of them.
func TestBuildRuntimeAcceptsDriverFacts(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("ARK_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			StableID:  "inst-openai",
			Type:      "openai",
			KeySource: config.KeyEnv,
			Enabled:   true,
			Advanced: config.InstanceAdvanced{
				Store: "omit",
				// A field the compiler does not lower: flowcraft
				// rejects extra_body keys that name a typed knob.
				ExtraBody: map[string]string{
					"custom_gateway_hint": `"prefer-a"`,
				},
			},
			Models: []config.Model{{
				Name:         "gpt-5.6-sol",
				Capabilities: model.ModelCapabilities{Outputs: []message.PartKind{message.PartText}},
				Lifecycle: config.ModelLifecycle{
					Status:              "deprecated",
					ReplacementProvider: "openai",
					ReplacementName:     "gpt-5.6-terra",
					Notes:               "sunset later this year",
				},
			}},
		}, {
			StableID:  "inst-ark",
			Type:      "bytedance",
			KeySource: config.KeyEnv,
			Enabled:   false,
			Models: []config.Model{{
				Name: "doubao-seedance-2-0",
				Kind: "video",
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
		}},
	}
	if err := config.WriteInference(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}

	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()
}
