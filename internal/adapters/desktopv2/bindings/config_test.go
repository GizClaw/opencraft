package bindings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
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

func TestMCPProbeStatusClassifiesTransientTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "ready", err: nil, want: "connected"},
		{name: "still connecting", err: errdefs.Timeoutf("not ready within 200ms"), want: "connecting"},
		{name: "rejected", err: errdefs.Validationf("server rejected connection"), want: "error"},
		{name: "closed", err: errdefs.NotAvailablef("source is closed"), want: "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpProbeStatus(tt.err); got != tt.want {
				t.Fatalf("mcpProbeStatus(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
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

	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi-main", Type: "deepseek", Name: "Haivivi SSO",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	if err := config.WriteInferenceOwned(dir, cfg, map[string]string{
		"sso-haivivi-main": "sso-haivivi",
	}); err != nil {
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
	if !managed["sso-haivivi-main"] {
		t.Fatalf("sso-haivivi-main should be managed: %+v", state.Instances)
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
		{StableID: "sso-haivivi-main", Type: "deepseek", Name: "Haivivi SSO",
			KeySource: config.KeyLiteral, KeyValue: "managed-key",
			Enabled: true,
			Models:  []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeyLiteral, KeyValue: "user-key", Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	if err := config.WriteInferenceOwned(dir, seed, map[string]string{
		"sso-haivivi-main": "sso-haivivi",
	}); err != nil {
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
		if in.StableID == "sso-haivivi-main" {
			managed = in
		}
	}
	if managed.StableID != "sso-haivivi-main" || !managed.Enabled ||
		managed.KeyValue != "managed-key" {
		t.Fatalf("managed instance not restored: %+v", managed)
	}
}

func TestRestoreManagedInstancesKeepsProviderSpec(t *testing.T) {
	spec := map[string]any{
		"chat_stream_options": map[string]any{
			"include_usage": false,
		},
	}
	model := config.Model{Name: "glm-5.3-flash"}
	existing := []config.Instance{{
		StableID:     "sso-haivivi-glm",
		Type:         "openai",
		Name:         "Haivivi SSO · GLM",
		API:          "chat",
		KeySource:    config.KeyKeychain,
		KeyValue:     "auth/sso-haivivi/token",
		Models:       []config.Model{model},
		ProviderSpec: spec,
		Enabled:      true,
	}}
	requested := []config.Instance{{
		StableID:  "sso-haivivi-glm",
		Type:      "openai",
		Name:      "Haivivi SSO · GLM",
		API:       "chat",
		KeySource: config.KeyKeychain,
		KeyValue:  "auth/sso-haivivi/token",
		Models:    []config.Model{model},
		Enabled:   true,
	}}
	out, restored := restoreManagedInstances(
		existing, requested,
		map[string]bool{"sso-haivivi-glm": true},
	)
	if len(out) != 1 || len(restored) != 0 {
		t.Fatalf("out = %+v restored = %v", out, restored)
	}
	if out[0].ProviderSpec == nil ||
		out[0].ProviderSpec["chat_stream_options"] == nil {
		t.Fatalf("managed provider spec dropped: %+v", out[0])
	}
}

func TestConfigStateMarksMultiplePluginOwnedInstances(t *testing.T) {
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi-main", Type: "deepseek", Name: "Main",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "sso-haivivi-gateway", Type: "deepseek",
			Name: "Gateway", KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "user-1", Type: "openai", Name: "User",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	owners := map[string]string{
		"sso-haivivi-main":    "sso-haivivi",
		"sso-haivivi-gateway": "sso-haivivi",
	}
	if err := config.WriteInferenceOwned(dir, cfg, owners); err != nil {
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
	if !managed["sso-haivivi-main"] || !managed["sso-haivivi-gateway"] {
		t.Fatalf("plugin-owned instances should be managed: %+v", state.Instances)
	}
	if managed["user-1"] {
		t.Fatalf("user row must not be managed: %+v", state.Instances)
	}
}

func TestSaveInstancesRestoresMultipleManagedRows(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	seed := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi-main", Type: "deepseek", Name: "Main",
			KeySource: config.KeyLiteral, KeyValue: "key-1", Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "sso-haivivi-gateway", Type: "deepseek",
			Name: "Gateway", KeySource: config.KeyLiteral,
			KeyValue: "key-2", Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "user-1", Type: "openai", Name: "User",
			KeySource: config.KeyLiteral, KeyValue: "user-key",
			Enabled: true,
			Models:  []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	owners := map[string]string{
		"sso-haivivi-main":    "sso-haivivi",
		"sso-haivivi-gateway": "sso-haivivi",
	}
	if err := config.WriteInferenceOwned(dir, seed, owners); err != nil {
		t.Fatal(err)
	}

	req := InferenceRequest{Instances: []ProviderInstance{
		{StableID: "user-1", Type: "openai", Name: "User",
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
	if len(cfg.Instances) != 3 {
		t.Fatalf("instances = %+v, want managed rows restored", cfg.Instances)
	}
	gotOwners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotOwners["sso-haivivi-main"] != "sso-haivivi" ||
		gotOwners["sso-haivivi-gateway"] != "sso-haivivi" {
		t.Fatalf("owners = %+v", gotOwners)
	}
}
