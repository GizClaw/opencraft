package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/plugins/runtime"
)

func TestInferenceProfileUpsertAndRemove(t *testing.T) {
	a := fileManagerApp(t, t.TempDir())
	profile := pluginruntime.InferenceProfile{
		ID:       "sso-haivivi",
		Type:     "openai",
		Name:     "Haivivi SSO",
		API:      "responses",
		Endpoint: "https://ai.haivivi.cn/v1",
		Models: []pluginruntime.ProfileModel{
			{Name: "deepseek-flash", Reasoning: "toggle", WebSearch: true},
			{Name: "deepseek-vision", Vision: true, Reasoning: "toggle", WebSearch: true},
		},
		KeyRef: "auth/sso-haivivi/token",
	}
	if err := a.upsertInferenceProfile("sso-haivivi", profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	in := cfg.Instances[0]
	if in.StableID != "sso-haivivi" || in.Type != "openai" || in.API != "responses" ||
		in.Endpoint != "https://ai.haivivi.cn/v1" ||
		in.KeySource != config.KeyKeychain ||
		in.KeyValue != "auth/sso-haivivi/token" ||
		len(in.Models) != 2 || !in.Enabled {
		t.Fatalf("inference instance = %+v", in)
	}
	if in.Models[0].Capabilities.Reasoning != inference.ReasoningToggle ||
		!in.Models[0].Capabilities.HostedWebSearch ||
		len(in.Models[0].Capabilities.Inputs) != 1 ||
		in.Models[0].Capabilities.Inputs[0] != message.PartText {
		t.Fatalf("model capabilities not carried through: %+v", in.Models[0])
	}
	if len(in.Models[1].Capabilities.Inputs) != 2 ||
		in.Models[1].Capabilities.Inputs[0] != message.PartText ||
		in.Models[1].Capabilities.Inputs[1] != message.PartImage {
		t.Fatalf("legacy vision capability not normalized: %+v", in.Models[1])
	}

	if err := a.removeInferenceProfile("sso-haivivi"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, _ = config.LoadInference(a.userDir)
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after remove = %+v", cfg.Instances)
	}
}

func TestInferenceProfileValidation(t *testing.T) {
	a := fileManagerApp(t, t.TempDir())
	base := pluginruntime.InferenceProfile{
		ID:       "sso-haivivi",
		Type:     "openai",
		Endpoint: "https://ai.haivivi.cn/v1",
		Models:   []pluginruntime.ProfileModel{{Name: "m"}},
		KeyRef:   "auth/sso-haivivi/token",
	}
	// Profile id must match the calling plugin.
	bad := base
	bad.ID = "other-plugin"
	if err := a.upsertInferenceProfile("sso-haivivi", bad); err == nil {
		t.Fatal("profile id mismatch must fail")
	}
	// Key ref must stay in the plugin namespace.
	bad = base
	bad.KeyRef = "auth/other-plugin/token"
	if err := a.upsertInferenceProfile("sso-haivivi", bad); err == nil {
		t.Fatal("key ref outside namespace must fail")
	}
	// Empty models must fail.
	bad = base
	bad.Models = nil
	if err := a.upsertInferenceProfile("sso-haivivi", bad); err == nil {
		t.Fatal("empty models must fail")
	}
	// Invalid endpoint must fail.
	bad = base
	bad.Endpoint = "not a url"
	if err := a.upsertInferenceProfile("sso-haivivi", bad); err == nil {
		t.Fatal("invalid endpoint must fail")
	}
	// Unknown type must fail.
	bad = base
	bad.Type = "nope"
	if err := a.upsertInferenceProfile("sso-haivivi", bad); err == nil {
		t.Fatal("unknown type must fail")
	}
}

func TestRestoreManagedInstances(t *testing.T) {
	existing := []config.Instance{
		{StableID: "sso-haivivi", Type: "deepseek", Name: "Haivivi SSO",
			API: "responses", Endpoint: "https://ai.haivivi.cn/v1",
			Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash",
				Capabilities: inference.ModelCapabilities{
					Outputs: []message.PartKind{message.PartText},
				}}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI", Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}
	managed := map[string]bool{"sso-haivivi": true}

	// Edited managed row: content must come back from the stored
	// config, position preserved.
	requested := []config.Instance{
		{StableID: "sso-haivivi", Type: "deepseek", Name: "Hacked",
			API: "chat", Enabled: false,
			Models: []config.Model{{Name: "other-model"}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI", Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}
	out, restored := restoreManagedInstances(existing, requested, managed)
	if len(restored) != 1 || restored[0] != "sso-haivivi" {
		t.Fatalf("restored = %v, want [sso-haivivi]", restored)
	}
	got := out[0]
	if got.Name != "Haivivi SSO" || got.API != "responses" || !got.Enabled ||
		len(got.Models) != 1 || got.Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("managed row not restored: %+v", got)
	}

	// Dropped managed row: re-appended from the stored config.
	requested = []config.Instance{
		{StableID: "user-1", Type: "openai", Name: "My OpenAI", Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}
	out, restored = restoreManagedInstances(existing, requested, managed)
	if len(restored) != 1 || restored[0] != "sso-haivivi" {
		t.Fatalf("restored = %v, want [sso-haivivi]", restored)
	}
	if len(out) != 2 || out[1].StableID != "sso-haivivi" {
		t.Fatalf("managed row not re-appended: %+v", out)
	}
}

func TestSaveInferenceRestoresManagedInstances(t *testing.T) {
	dir := t.TempDir()
	a := fileManagerApp(t, dir)
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginDir, "sso-haivivi"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"sso-haivivi","name":"Haivivi SSO","version":"1.0.0","entry":"index.js","permissions":[]}`
	if err := os.WriteFile(
		filepath.Join(pluginDir, "sso-haivivi", "plugin.json"),
		[]byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	a.plugins = plugins.NewStore(pluginDir)

	seed := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi", Type: "deepseek", Name: "Haivivi SSO",
			API: "responses", Endpoint: "https://ai.haivivi.cn/v1",
			KeySource: config.KeyKeychain, KeyValue: "auth/sso-haivivi/token",
			Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash",
				Capabilities: inference.ModelCapabilities{
					Outputs: []message.PartKind{message.PartText},
				}}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeyLiteral, KeyValue: "sk-user", Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	if err := config.WriteInference(dir, seed); err != nil {
		t.Fatal(err)
	}

	// The settings page drops the managed row; the save must keep it.
	req := InferenceRequest{Instances: []ProviderInstance{
		{StableID: "user-1", Type: "openai", Name: "My OpenAI", Enabled: true,
			Models: []ModelView{{Name: "gpt-5.6-sol"}}},
	}}
	if err := a.saveInference(req); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 2 {
		t.Fatalf("instances = %+v, want managed row restored", cfg.Instances)
	}
	var managed config.Instance
	for _, in := range cfg.Instances {
		if in.StableID == "sso-haivivi" {
			managed = in
		}
	}
	if managed.StableID != "sso-haivivi" || managed.Type != "deepseek" ||
		!managed.Enabled || len(managed.Models) != 1 ||
		managed.Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("managed instance not restored: %+v", managed)
	}
}
