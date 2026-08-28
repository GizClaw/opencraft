package desktop

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/config"
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
	if in.Models[0].Reasoning != "toggle" || !in.Models[0].WebSearch || in.Models[0].Vision {
		t.Fatalf("model capabilities not carried through: %+v", in.Models[0])
	}
	if !in.Models[1].Vision {
		t.Fatalf("vision capability not carried through: %+v", in.Models[1])
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
