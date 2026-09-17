package bindings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
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

// TestInferenceCatalogResolvesTemplates pins the catalog binding the
// settings page reads: every template must arrive with its models
// already resolved, and every model must name a provider the instance
// picker can offer.
func TestInferenceCatalogResolvesTemplates(t *testing.T) {
	b := NewConfig(core.NewCore(t.TempDir(), t.TempDir(), ""))
	st, err := b.InferenceCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if st.Version == "" {
		t.Fatal("catalog has no version")
	}
	if len(st.Templates) == 0 || len(st.Models) == 0 {
		t.Fatalf(
			"catalog is empty: %d templates, %d models",
			len(st.Templates), len(st.Models),
		)
	}
	providers := b.Providers()
	known := make(map[string]bool, len(providers))
	for _, p := range providers {
		known[p.ID] = true
	}
	for _, m := range st.Models {
		if !known[m.Type] {
			t.Fatalf("catalog model %s names unknown type %q", m.ID, m.Type)
		}
		if strings.TrimSpace(m.Model.Name) == "" {
			t.Fatalf("catalog model %s carries no model name", m.ID)
		}
	}
	for _, template := range st.Templates {
		if len(template.Models) == 0 {
			t.Fatalf("template %s resolved no models", template.ID)
		}
		for _, m := range template.Models {
			if strings.TrimSpace(m.Name) == "" {
				t.Fatalf("template %s carries a nameless model", template.ID)
			}
		}
	}
}

func TestModelUsageRequiresUserDB(t *testing.T) {
	b := NewConfig(core.NewCore(t.TempDir(), t.TempDir(), ""))
	if _, err := b.ModelUsage(); err == nil {
		t.Fatal("ModelUsage succeeded before OpenUserDB, want not-ready error")
	}
	if _, err := b.ModelUsageSeries(
		"openai-1/gpt-test", "hour", 0, "", "",
	); err == nil {
		t.Fatal("ModelUsageSeries succeeded before OpenUserDB, want not-ready error")
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
	t.Setenv("OPENAI_API_KEY", "test-key")
	dir := t.TempDir()
	b := NewConfig(core.NewCore(dir, dir, ""))
	err := b.SaveInstances(InferenceRequest{Instances: []config.InstanceSpec{{
		Type:      "openai",
		Name:      "primary",
		API:       "chat",
		KeySource: config.KeySourceEnvName,
		Enabled:   boolPtr(true),
		Models:    []config.ModelSpec{{Name: "deepseek-v4-flash"}},
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
			Type:      "openai",
			Name:      "Primary",
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models: []config.Model{{
				Name: "deepseek-v4-flash",
				Capabilities: model.ModelCapabilities{
					Reasoning: model.ReasoningCapability{
						Kind: model.ReasoningAlways,
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

// TestModelOptionsOffersOnlyChatModels pins the composer/automation
// model picker to models a chat turn can run on: image- and video-only
// rows stay router targets for generate_image/generate_video but must
// not be selectable, because the router would silently route the turn
// around the hint.
func TestModelOptionsOffersOnlyChatModels(t *testing.T) {
	dir := t.TempDir()
	b := NewConfig(core.NewCore(dir, dir, ""))
	if err := config.WriteInference(dir, config.InferenceConfig{
		Instances: []config.Instance{
			{
				StableID:  "primary",
				Type:      "openai",
				Name:      "Primary",
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models: []config.Model{
					{Name: "gpt-image-2", Kind: "image",
						Capabilities: model.ModelCapabilities{
							Outputs: []message.PartKind{message.PartImage},
						}},
					{Name: "gpt-5",
						Capabilities: model.ModelCapabilities{
							Outputs: []message.PartKind{message.PartText},
						}},
					{Name: "gpt-5-image",
						Capabilities: model.ModelCapabilities{
							Outputs: []message.PartKind{
								message.PartText, message.PartImage,
							},
						}},
				},
			},
			{
				StableID:  "media",
				Type:      "minimax",
				Name:      "Media",
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models: []config.Model{
					{Name: "image-01", Kind: "image",
						Capabilities: model.ModelCapabilities{
							Outputs: []message.PartKind{message.PartImage},
						}},
					{Name: "video-01", Kind: "video",
						Capabilities: model.ModelCapabilities{
							Outputs: []message.PartKind{message.PartVideo},
						}},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	options, err := b.ModelOptions()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range options {
		ids = append(ids, o.ID)
	}
	want := []string{"openai-primary/gpt-5", "openai-primary/gpt-5-image"}
	if len(ids) != len(want) {
		t.Fatalf("model options = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("model options = %v, want %v", ids, want)
		}
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

// boolPtr returns a pointer to v for the optional spec fields.
func boolPtr(v bool) *bool { return &v }

func TestConfigStateMarksPluginManagedInstances(t *testing.T) {
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi-main", Type: "openai", Name: "Haivivi SSO",
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
		{StableID: "sso-haivivi-main", Type: "openai", Name: "Haivivi SSO",
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

	req := InferenceRequest{Instances: []config.InstanceSpec{
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeySourceEnvName, Enabled: boolPtr(true),
			Models: []config.ModelSpec{{Name: "gpt-5.6-sol"}}},
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

// TestSaveInstancesRestoresManagedContent pins the plugin-owned row
// guarantee at the binding level: an edit to a managed row does not
// stick, its stored content (provider knobs included) comes back, and
// the settings page can tell why.
func TestSaveInstancesRestoresManagedContent(t *testing.T) {
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	includeUsage := false
	stored := config.Instance{
		StableID:  "sso-haivivi-glm",
		Type:      "openai",
		Name:      "Haivivi SSO · GLM",
		API:       "chat",
		KeySource: config.KeyKeychain,
		KeyValue:  "auth/sso-haivivi/token",
		Advanced:  config.InstanceAdvanced{ChatIncludeUsage: &includeUsage},
		Models:    []config.Model{{Name: "glm-5.3-flash"}},
		Enabled:   true,
	}
	user := config.Instance{
		StableID: "user-1", Type: "openai", KeySource: config.KeyEnv,
		Enabled: true, Models: []config.Model{{Name: "gpt-5.6-sol"}},
	}
	if err := config.WriteInferenceOwned(
		dir,
		config.InferenceConfig{Instances: []config.Instance{stored, user}},
		map[string]string{"sso-haivivi-glm": "sso-haivivi"},
	); err != nil {
		t.Fatal(err)
	}

	// The page tries to edit the managed row's model.
	spec := config.InstanceToSpec(stored)
	spec.Models = []config.ModelSpec{{Name: "tampered"}}
	if err := b.SaveInstances(InferenceRequest{
		Instances: []config.InstanceSpec{spec},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	var managed config.Instance
	for _, in := range cfg.Instances {
		if in.StableID == "sso-haivivi-glm" {
			managed = in
		}
	}
	if managed.StableID != "sso-haivivi-glm" ||
		len(managed.Models) != 1 ||
		managed.Models[0].Name != "glm-5.3-flash" {
		t.Fatalf("managed row edit stuck: %+v", managed)
	}
	if managed.Advanced.ChatIncludeUsage == nil ||
		*managed.Advanced.ChatIncludeUsage {
		t.Fatalf("managed provider knobs dropped: %+v", managed.Advanced)
	}
}

func TestConfigStateMarksMultiplePluginOwnedInstances(t *testing.T) {
	dir := t.TempDir()
	writePluginManifest(t, dir, "sso-haivivi")
	b := NewConfig(core.NewCore(dir, dir, ""))

	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "sso-haivivi-main", Type: "openai", Name: "Main",
			KeySource: config.KeyEnv, Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "sso-haivivi-gateway", Type: "openai",
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
		{StableID: "sso-haivivi-main", Type: "openai", Name: "Main",
			KeySource: config.KeyLiteral, KeyValue: "key-1", Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: "sso-haivivi-gateway", Type: "openai",
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

	req := InferenceRequest{Instances: []config.InstanceSpec{
		{StableID: "user-1", Type: "openai", Name: "User",
			KeySource: config.KeySourceEnvName, Enabled: boolPtr(true),
			Models: []config.ModelSpec{{Name: "gpt-5.6-sol"}}},
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

// TestSaveInstancesKeepsPluginDeclaredVendorRow pins the settings page
// round trip in the presence of a provider whose type is not in the
// built-in catalog: a plugin-declared vendor (type + explicit driver)
// must not block the user's save, and its identity must survive.
func TestSaveInstancesKeepsPluginDeclaredVendorRow(t *testing.T) {
	dir := t.TempDir()
	writePluginManifest(t, dir, "vendorx")
	b := NewConfig(core.NewCore(dir, dir, ""))

	seed := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "vendorx-main", Type: "vendorx", Driver: "openai",
			Name: "Vendor X", API: "chat",
			Endpoint:  "https://api.vendorx.example/v1",
			KeySource: config.KeyKeychain, KeyValue: "auth/vendorx/token",
			Enabled: true,
			Models:  []config.Model{{Name: "vendorx-pro"}}},
		{StableID: "user-1", Type: "openai", Name: "My OpenAI",
			KeySource: config.KeyKeychain, KeyValue: "provider/openai-1/1",
			Enabled: true,
			Models:  []config.Model{{Name: "gpt-5.6-sol"}}},
	}}
	if err := config.WriteInferenceOwned(dir, seed, map[string]string{
		"vendorx-main": "vendorx",
	}); err != nil {
		t.Fatal(err)
	}

	// The settings page round-trips exactly what ConfigState returned.
	state, err := b.ConfigState()
	if err != nil {
		t.Fatal(err)
	}
	specs := make([]config.InstanceSpec, 0, len(state.Instances))
	for _, view := range state.Instances {
		specs = append(specs, view.InstanceSpec)
	}
	req := InferenceRequest{Instances: specs, Router: state.Router}
	if err := b.SaveInstances(req); err != nil {
		t.Fatalf("save with plugin-declared vendor row: %v", err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	var vendor *config.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].StableID == "vendorx-main" {
			vendor = &cfg.Instances[i]
		}
	}
	if vendor == nil {
		t.Fatalf("plugin-declared row dropped: %+v", cfg.Instances)
	}
	if vendor.Type != "vendorx" || vendor.Driver != "openai" {
		t.Fatalf("plugin-declared row degraded: %+v", vendor)
	}
}
