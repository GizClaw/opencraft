package bindings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestConfigMemoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b := NewConfig(core.NewCore(dir, dir, ""))

	if err := b.SaveMemory(config.MemorySettings{
		MaxRawMessages:  48,
		PreserveRecent:  6,
		MaxSummaryBytes: 8192,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := b.MemoryConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxRawMessages != 48 || got.PreserveRecent != 6 {
		t.Fatalf("memory config = %+v", got)
	}
}

func TestConfigSaveInstances(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	dir := t.TempDir()
	b := NewConfig(core.NewCore(dir, dir, ""))
	err := b.SaveInstances(InferenceRequest{Instances: []ProviderInstance{{
		Type:    "deepseek",
		Name:    "primary",
		API:     "chat",
		KeyEnv:  true,
		Enabled: true,
		Models:  []ModelView{{Name: "deepseek-v4-flash"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := b.ConfigState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Instances) != 1 ||
		len(state.Instances[0].Models) != 1 ||
		state.Instances[0].Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("config state = %+v", state)
	}
}

func TestConfigStatusReportsDefaultReasoning(t *testing.T) {
	dir := t.TempDir()
	b := NewConfig(core.NewCore(dir, dir, ""))
	if err := config.WriteInference(dir, config.InferenceConfig{
		Instances: []config.Instance{{
			StableID:  "primary",
			Type:      "deepseek",
			Name:      "Primary",
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models: []config.Model{{
				Name: "deepseek-v4-flash",
				Capabilities: inference.ModelCapabilities{
					Reasoning: inference.ReasoningCapability{
						Kind: inference.ReasoningAlways,
					},
				},
			}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	status, err := b.ConfigStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Needed {
		t.Fatalf("status = %+v, want configured", status)
	}
	if !status.DefaultReasoning {
		t.Fatal("default reasoning must reflect the router model capability")
	}
	if status.DefaultModel == "" {
		t.Fatal("default model must be reported")
	}
}

func writePluginManifest(t *testing.T, dataDir, id string) {
	t.Helper()
	dir := filepath.Join(dataDir, "plugins", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"` + id + `","name":"` + id +
		`","version":"1.0.0","entry":"index.js","permissions":[]}`
	if err := os.WriteFile(
		filepath.Join(dir, "plugin.json"),
		[]byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func TestConfigStateMarksPluginManagedInstances(t *testing.T) {
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	if err := config.WriteInference(dir, config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi", Type: "deepseek", Name: "Haivivi SSO",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}}); err != nil {
		t.Fatal(err)
	}

	state, err := b.ConfigState()
	if err != nil {
		t.Fatal(err)
	}
	managed := map[string]bool{}
	for _, in := range state.Instances {
		managed[in.StableID] = in.Managed
	}
	if !managed["sso-haivivi"] {
		t.Fatalf("sso-haivivi should be managed: %+v", state.Instances)
	}
	if managed["user-1"] {
		t.Fatalf("user-1 should not be managed: %+v", state.Instances)
	}
}

func TestSaveInstancesRestoresManagedRows(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	seed := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi", Type: "deepseek", Name: "Haivivi SSO",
			KeySource: config.KeyLiteral, KeyValue: "managed-key",
			Enabled: true,
			Models:  []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeyLiteral, KeyValue: "user-key", Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	if err := config.WriteInference(dir, seed); err != nil {
		t.Fatal(err)
	}

	req := InferenceRequest{Instances: []ProviderInstance{
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeyEnv: true, Enabled: true,
			Models: []ModelView{{Name: "gpt-5.6-sol"}}},
	}}
	if err := b.SaveInstances(req); err != nil {
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
	if managed.StableID != "sso-haivivi" || !managed.Enabled ||
		managed.KeyValue != "managed-key" {
		t.Fatalf("managed instance not restored: %+v", managed)
	}
}
