package core

import (
	"testing"

	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestPluginInferenceUpsertAndRemove(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")

	profile := pluginruntime.InferenceProfile{
		ID:       "sso-haivivi",
		Type:     "deepseek",
		Name:     "Haivivi SSO",
		API:      "responses",
		Endpoint: "https://ai.haivivi.cn/v1",
		Models: []pluginruntime.ProfileModel{
			{Name: "deepseek-v4-flash", Reasoning: "toggle"},
		},
		KeyRef: "auth/sso-haivivi/token",
	}
	if err := c.upsertInferenceProfile("sso-haivivi", profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	in := cfg.Instances[0]
	if in.StableID != "sso-haivivi" || in.Type != "deepseek" ||
		!in.Enabled || in.KeySource != config.KeyKeychain ||
		in.KeyValue != "auth/sso-haivivi/token" ||
		len(in.Models) != 1 || in.Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("inference instance = %+v", in)
	}

	if err := c.removeInferenceProfile("sso-haivivi"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, err = config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after remove = %+v", cfg.Instances)
	}
}

func TestPluginInferenceRejectsForeignProfile(t *testing.T) {
	c := NewCore(t.TempDir(), t.TempDir(), "")
	profile := pluginruntime.InferenceProfile{
		ID:     "other-plugin",
		Type:   "deepseek",
		Models: []pluginruntime.ProfileModel{{Name: "deepseek-v4-flash"}},
		KeyRef: "auth/sso-haivivi/token",
	}
	if err := c.upsertInferenceProfile("sso-haivivi", profile); err == nil {
		t.Fatal("foreign profile id must fail")
	}
}
