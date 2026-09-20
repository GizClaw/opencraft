package bindings

import (
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
)

// toolOptionsBinding writes one image-capable and one video-capable
// deployment and returns the binding over it.
func toolOptionsBinding(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	if err := configseed.Write(dir, config.InferenceConfig{
		Instances: []config.Instance{
			{
				StableID:  "img",
				Type:      "openai",
				Name:      "OpenAI Images",
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models: []config.Model{{
					Name: "gpt-image-2",
					Capabilities: model.ModelCapabilities{
						Outputs: []message.PartKind{message.PartImage},
					},
				}},
			},
			{
				StableID:  "vid",
				Type:      "bytedance",
				Name:      "Seedance",
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models: []config.Model{{
					Name: "doubao-seedance-2-0",
					Capabilities: model.ModelCapabilities{
						Outputs: []message.PartKind{message.PartVideo},
					},
				}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return NewConfig(core.NewCore(dir, dir, ""))
}

func TestToolOptionsListsPerToolInstances(t *testing.T) {
	b := toolOptionsBinding(t)
	state, err := b.ToolOptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Image.Instances) != 1 {
		t.Fatalf("image instances = %+v", state.Image.Instances)
	}
	image := state.Image.Instances[0]
	if image.ID != "openai-img" || image.Impl != "openai" ||
		image.Label != "OpenAI" {
		t.Fatalf("image instance = %+v", image)
	}
	var names []string
	for _, field := range image.Fields {
		names = append(names, field.Name)
	}
	for _, want := range []string{
		"background", "output_compression", "input_fidelity", "moderation",
	} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("image fields = %v, want %s", names, want)
		}
	}
	if len(image.Values) != 0 {
		t.Errorf("image values = %+v, want empty", image.Values)
	}
	if len(state.Video.Instances) != 1 {
		t.Fatalf("video instances = %+v", state.Video.Instances)
	}
	video := state.Video.Instances[0]
	if video.ID != "bytedance-vid" || video.Impl != "bytedance" {
		t.Fatalf("video instance = %+v", video)
	}
	if len(video.Fields) == 0 {
		t.Fatal("video instance has no configurable fields")
	}
	// A text-only deployment belongs to neither card.
	if len(state.Image.Instances)+len(state.Video.Instances) != 2 {
		t.Fatalf("cards = %+v", state)
	}
}

func TestSaveToolOptionsRoundTrip(t *testing.T) {
	b := toolOptionsBinding(t)
	err := b.SaveToolOptions(ToolOptionsRequest{
		Image: map[string]map[string]any{
			"openai-img": {
				"background":         "transparent",
				"output_compression": 80,
			},
		},
		Video: map[string]map[string]any{
			"bytedance-vid": {"camera_fixed": true, "service_tier": "flex"},
		},
	})
	if err != nil {
		t.Fatalf("SaveToolOptions: %v", err)
	}
	state, err := b.ToolOptions()
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Image.Instances[0].Values["background"]; got != "transparent" {
		t.Fatalf("image values = %+v", state.Image.Instances[0].Values)
	}
	if got := state.Video.Instances[0].Values["service_tier"]; got != "flex" {
		t.Fatalf("video values = %+v", state.Video.Instances[0].Values)
	}

	// The stored shape is the driver-native nested object.
	stored, err := config.LoadToolOptions(b.core.UserDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Image["openai-img"]["background"] != "transparent" {
		t.Fatalf("stored = %+v", stored)
	}

	for _, tc := range []struct {
		name string
		req  ToolOptionsRequest
		want string
	}{
		{"unknown provider", ToolOptionsRequest{
			Image: map[string]map[string]any{"nope": {"background": "auto"}},
		}, "unknown provider"},
		{"unknown field", ToolOptionsRequest{
			Image: map[string]map[string]any{"openai-img": {"nope": true}},
		}, "unknown option"},
		{"bad value", ToolOptionsRequest{
			Video: map[string]map[string]any{"bytedance-vid": {"priority": 42}},
		}, "must be at most 9"},
		{"wrong card", ToolOptionsRequest{
			Video: map[string]map[string]any{"openai-img": {"output_format": "mp4"}},
		}, "does not support video-specific options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := b.SaveToolOptions(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
