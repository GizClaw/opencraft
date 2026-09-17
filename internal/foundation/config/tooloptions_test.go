package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
)

// toolOptionInstances is one OpenAI and one ByteDance deployment, the
// two providers the image vocabulary models.
func toolOptionInstances() []Instance {
	return []Instance{
		{
			StableID:  "a",
			Type:      "openai",
			KeySource: KeyEnv,
			Enabled:   true,
			Models: []Model{{
				Name: "gpt-image-2",
				Capabilities: model.ModelCapabilities{
					Outputs: []message.PartKind{message.PartImage},
				},
			}},
		},
		{
			StableID:  "b",
			Type:      "bytedance",
			KeySource: KeyEnv,
			Enabled:   true,
			Models: []Model{{
				Name: "doubao-seedream-4-0",
				Capabilities: model.ModelCapabilities{
					Outputs: []message.PartKind{message.PartImage},
				},
			}},
		},
	}
}

func TestToolOptionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	instances := toolOptionInstances()
	if err := WriteInference(dir, InferenceConfig{Instances: instances}); err != nil {
		t.Fatal(err)
	}
	opts := ToolOptions{
		Image: map[string]map[string]any{
			"openai-a": {
				"background":         "transparent",
				"output_compression": 80,
			},
			"bytedance-b": {
				"size_token": "2k",
				"optimize_prompt": map[string]any{
					"mode": "fast",
				},
			},
		},
		Video: map[string]map[string]any{
			"bytedance-b": {"camera_fixed": true},
		},
	}
	if err := SaveToolOptions(dir, opts, instances); err != nil {
		t.Fatalf("SaveToolOptions: %v", err)
	}
	got, err := LoadToolOptions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Image["openai-a"]["background"] != "transparent" {
		t.Errorf("openai options = %+v", got.Image["openai-a"])
	}
	if got.Image["bytedance-b"]["size_token"] != "2k" {
		t.Errorf("bytedance options = %+v", got.Image["bytedance-b"])
	}
	if nested, ok := got.Image["bytedance-b"]["optimize_prompt"].(map[string]any); !ok ||
		nested["mode"] != "fast" {
		t.Errorf("nested options = %+v", got.Image["bytedance-b"])
	}
	if got.Video["bytedance-b"]["camera_fixed"] != true {
		t.Errorf("video options = %+v", got.Video)
	}

	// The inference wiring the settings page owns survives the write.
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != len(instances) {
		t.Fatalf("instances = %+v, want the saved rows", cfg.Instances)
	}
}

func TestSaveToolOptionsClearsRemovedBlocks(t *testing.T) {
	dir := t.TempDir()
	instances := toolOptionInstances()
	if err := WriteInference(dir, InferenceConfig{Instances: instances}); err != nil {
		t.Fatal(err)
	}
	if err := SaveToolOptions(dir, ToolOptions{
		Image: map[string]map[string]any{"openai-a": {"moderation": "low"}},
	}, instances); err != nil {
		t.Fatal(err)
	}
	if err := SaveToolOptions(dir, ToolOptions{}, instances); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tool.imagegen") ||
		strings.Contains(string(data), "provider_options") {
		t.Fatalf("cleared options must be removed:\n%s", data)
	}
	got, err := LoadToolOptions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("options = %+v, want empty", got)
	}
}

// TestToolOptionsReachTheDeployDocument pins the read path the tools
// depend on: the user layer's blocks merge onto the embedded tool
// resources, so the assembly hands them to the tool factories.
func TestToolOptionsReachTheDeployDocument(t *testing.T) {
	dir := t.TempDir()
	instances := toolOptionInstances()
	if err := WriteInference(dir, InferenceConfig{Instances: instances}); err != nil {
		t.Fatal(err)
	}
	if err := SaveToolOptions(dir, ToolOptions{
		Image: map[string]map[string]any{"openai-a": {"moderation": "low"}},
		Video: map[string]map[string]any{"bytedance-b": {"camera_fixed": true}},
	}, instances); err != nil {
		t.Fatal(err)
	}
	view := load(t, t.TempDir(), dir)
	for _, tc := range []struct {
		resource string
		want     string
	}{
		{"tool.imagegen", `"moderation":"low"`},
		{"tool.videogen", `"camera_fixed":true`},
	} {
		res, ok := view.Document.Resources[tc.resource]
		if !ok {
			t.Fatalf("%s missing from the merged document", tc.resource)
		}
		settings := string(res.Settings)
		settings = strings.ReplaceAll(settings, " ", "")
		if !strings.Contains(settings, `"provider_options"`) ||
			!strings.Contains(settings, tc.want) {
			t.Errorf("%s settings = %s, want %s", tc.resource, settings, tc.want)
		}
	}
	// The embedded wiring (deps, impl) survives the user-layer merge.
	if res := view.Document.Resources["tool.imagegen"]; res.Impl != "opencraft/imagegen" {
		t.Errorf("tool.imagegen impl = %q", res.Impl)
	}
}

func TestValidateToolOptions(t *testing.T) {
	instances := append(toolOptionInstances(), Instance{
		StableID:  "c",
		Type:      "minimax",
		KeySource: KeyEnv,
		Enabled:   true,
		Models: []Model{{
			Name: "image-01",
			Capabilities: model.ModelCapabilities{
				Outputs: []message.PartKind{message.PartImage},
			},
		}},
	})
	for _, tc := range []struct {
		name string
		opts ToolOptions
		want string
	}{
		{"unknown provider", ToolOptions{
			Image: map[string]map[string]any{"nope": {"background": "auto"}},
		}, "unknown provider"},
		{"provider without vocabulary", ToolOptions{
			Image: map[string]map[string]any{"minimax-c": {"background": "auto"}},
		}, "does not support image-specific options"},
		{"unknown field", ToolOptions{
			Image: map[string]map[string]any{"openai-a": {"nope": true}},
		}, `unknown option "nope"`},
		{"bad enum", ToolOptions{
			Image: map[string]map[string]any{"openai-a": {"background": "blue"}},
		}, "must be one of auto, opaque, transparent"},
		{"bad type", ToolOptions{
			Image: map[string]map[string]any{"openai-a": {"background": 3}},
		}, "must be a string"},
		{"out of range", ToolOptions{
			Image: map[string]map[string]any{"openai-a": {"output_compression": 200}},
		}, "must be at most 100"},
		{"nested leaf where object expected", ToolOptions{
			Image: map[string]map[string]any{"bytedance-b": {"optimize_prompt": "fast"}},
		}, `option "optimize_prompt" must be an object`},
		{"fractional int", ToolOptions{
			Image: map[string]map[string]any{"bytedance-b": {"sequential_max_images": 2.5}},
		}, "must be a whole number"},
		{"positive float", ToolOptions{
			Image: map[string]map[string]any{"bytedance-b": {"guidance_scale": 0}},
		}, "must be greater than 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateToolOptions(tc.opts, instances)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestNestAndFlattenToolOptions(t *testing.T) {
	schema := ToolOptionSchema(ToolImage, "bytedance")
	flat := map[string]any{
		"optimize_prompt.mode":     "fast",
		"optimize_prompt.thinking": "enabled",
		"size_token":               "2k",
	}
	nested, err := NestToolOptions(schema, flat)
	if err != nil {
		t.Fatalf("NestToolOptions: %v", err)
	}
	optimize, ok := nested["optimize_prompt"].(map[string]any)
	if !ok || optimize["mode"] != "fast" || optimize["thinking"] != "enabled" {
		t.Fatalf("nested = %+v", nested)
	}
	back := FlattenToolOptions(schema, nested)
	for name, want := range flat {
		if back[name] != want {
			t.Errorf("flattened %s = %v, want %v", name, back[name], want)
		}
	}
	if _, err := NestToolOptions(schema, map[string]any{"nope": 1}); err == nil {
		t.Fatal("unknown path must be rejected")
	}
	if _, err := NestToolOptions(
		schema, map[string]any{"size_token": "7k"},
	); err == nil {
		t.Fatal("unknown enum value must be rejected")
	}
}

func TestModelServesImageAndVideo(t *testing.T) {
	image := Model{Capabilities: model.ModelCapabilities{
		Outputs: []message.PartKind{message.PartImage},
	}}
	video := Model{Capabilities: model.ModelCapabilities{
		Outputs: []message.PartKind{message.PartVideo},
	}}
	plain := Model{Capabilities: model.ModelCapabilities{
		Outputs: []message.PartKind{message.PartText},
	}}
	if !image.ServesImage() || image.ServesVideo() {
		t.Errorf("image model = %+v", image)
	}
	if !video.ServesVideo() || video.ServesImage() {
		t.Errorf("video model = %+v", video)
	}
	if plain.ServesImage() || plain.ServesVideo() {
		t.Errorf("text model = %+v", plain)
	}
}
