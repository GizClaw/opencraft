package core

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// TestRemovePluginInferenceValidatesPluginID pins the rule that stayed
// in the adapter: the plugin registry id is validated before the config
// layer is touched.
func TestRemovePluginInferenceValidatesPluginID(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")
	if _, err := c.RemovePluginInference("Bad Id"); err == nil {
		t.Fatal("invalid plugin id accepted")
	}
}

// TestRemovePluginInferenceDelegatesToConfig pins the cleanup path a
// plugin disable/uninstall takes: every row the plugin owns disappears
// and the ownership sidecar is cleaned up.
func TestRemovePluginInferenceDelegatesToConfig(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")
	includeUsage := false
	for _, id := range []string{"sso-haivivi-main", "sso-haivivi-glm"} {
		profile := config.InstanceSpec{
			StableID: id,
			Type:     "openai",
			API:      "chat",
			Endpoint: "https://ai.example.com/v1",
			KeyRef:   "auth/sso-haivivi/token",
			Advanced: config.InstanceAdvanced{ChatIncludeUsage: &includeUsage},
			Models:   []config.ModelSpec{{Name: "deepseek-v4-flash"}},
		}
		if err := config.UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	removed, err := c.RemovePluginInference("sso-haivivi")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("RemovePluginInference must report a removed row")
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after plugin remove = %+v", cfg.Instances)
	}
	owners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after plugin remove = %+v", owners)
	}
}
