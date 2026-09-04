package core

import (
	"strings"
	"testing"

	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestPluginInferenceUpsertAndRemove(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")

	profile := pluginruntime.InferenceProfile{
		ID:       "sso-haivivi-main",
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
	if in.StableID != "sso-haivivi-main" || in.Type != "deepseek" ||
		!in.Enabled || in.KeySource != config.KeyKeychain ||
		in.KeyValue != "auth/sso-haivivi/token" ||
		len(in.Models) != 1 || in.Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("inference instance = %+v", in)
	}
	owners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owners["sso-haivivi-main"] != "sso-haivivi" {
		t.Fatalf("owner not recorded: %+v", owners)
	}

	if err := c.removeInferenceProfile("sso-haivivi", "sso-haivivi-main"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, err = config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after remove = %+v", cfg.Instances)
	}
	owners, err = config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after remove = %+v, want empty", owners)
	}
}

func TestPluginInferenceMultipleProviders(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")

	base := pluginruntime.InferenceProfile{
		Type:   "deepseek",
		Models: []pluginruntime.ProfileModel{{Name: "deepseek-v4-flash"}},
		KeyRef: "auth/sso-haivivi/token",
	}
	for _, id := range []string{"sso-haivivi-gateway", "sso-haivivi-embed"} {
		p := base
		p.ID = id
		p.Name = id
		if err := c.upsertInferenceProfile("sso-haivivi", p); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 2 {
		t.Fatalf("instances = %+v, want two", cfg.Instances)
	}
	owners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owners["sso-haivivi-gateway"] != "sso-haivivi" ||
		owners["sso-haivivi-embed"] != "sso-haivivi" {
		t.Fatalf("owners = %+v", owners)
	}

	if err := c.removeInferenceProfile(
		"sso-haivivi", "sso-haivivi-gateway",
	); err != nil {
		t.Fatalf("remove one: %v", err)
	}
	cfg, err = config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 ||
		cfg.Instances[0].StableID != "sso-haivivi-embed" {
		t.Fatalf("instances after remove = %+v", cfg.Instances)
	}

	removed, err := c.RemovePluginInference("sso-haivivi")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("RemovePluginInference must report a removed row")
	}
	cfg, err = config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after plugin remove = %+v", cfg.Instances)
	}
	owners, err = config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after plugin remove = %+v", owners)
	}
}

func TestPluginInferenceProviderSpecWritesChatStreamOptions(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")

	profile := pluginruntime.InferenceProfile{
		ID:       "sso-haivivi-glm",
		Type:     "openai",
		Name:     "Haivivi SSO · GLM",
		API:      "chat",
		Endpoint: "https://ai.haivivi.cn/v1",
		Models: []pluginruntime.ProfileModel{
			{Name: "glm-5.3-flash", Reasoning: "always"},
		},
		KeyRef: "auth/sso-haivivi/token",
		ProviderSpec: map[string]any{
			"chat_stream_options": map[string]any{
				"include_usage": false,
			},
		},
	}
	if err := c.upsertInferenceProfile("sso-haivivi", profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v, want one GLM instance", cfg.Instances)
	}
	opts, ok := cfg.Instances[0].ProviderSpec["chat_stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != false {
		t.Fatalf("provider spec = %+v", cfg.Instances[0].ProviderSpec)
	}

	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"api: 'chat'",
		"chat_stream_options:",
		"include_usage: false",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("GLM spec missing %q:\n%s", want, doc)
		}
	}
}

func TestPluginInferenceProviderSpecValidation(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")

	base := pluginruntime.InferenceProfile{
		ID:       "sso-haivivi-glm",
		Type:     "openai",
		API:      "chat",
		Endpoint: "https://ai.haivivi.cn/v1",
		Models:   []pluginruntime.ProfileModel{{Name: "glm-5.3-flash"}},
		KeyRef:   "auth/sso-haivivi/token",
		ProviderSpec: map[string]any{
			"chat_stream_options": map[string]any{"include_usage": false},
		},
	}

	if err := c.upsertInferenceProfile("sso-haivivi", base); err != nil {
		t.Fatalf("valid openai chat profile rejected: %v", err)
	}

	badChat := base
	badChat.Type = "deepseek"
	if err := c.upsertInferenceProfile(
		"sso-haivivi", badChat,
	); err == nil || !strings.Contains(err.Error(), "openai chat") {
		t.Fatalf("chat_stream_options on non-openai profile = %v", err)
	}

	badAPI := base
	badAPI.API = "responses"
	if err := c.upsertInferenceProfile(
		"sso-haivivi", badAPI,
	); err == nil || !strings.Contains(err.Error(), "openai chat") {
		t.Fatalf("chat_stream_options on responses profile = %v", err)
	}

	managed := base
	managed.ProviderSpec = map[string]any{
		"api": "chat",
	}
	if err := c.upsertInferenceProfile(
		"sso-haivivi", managed,
	); err == nil || !strings.Contains(err.Error(), "managed by the host") {
		t.Fatalf("host-managed provider_spec key = %v", err)
	}
}

func TestPluginInferenceCannotHijackAnotherInstance(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")

	profile := pluginruntime.InferenceProfile{
		ID:     "sso-haivivi-gateway",
		Type:   "deepseek",
		Models: []pluginruntime.ProfileModel{{Name: "deepseek-v4-flash"}},
		KeyRef: "auth/sso-haivivi/token",
	}
	if err := c.upsertInferenceProfile("sso-haivivi", profile); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	other := profile
	other.KeyRef = "auth/other-plugin/token"
	if err := c.upsertInferenceProfile("other-plugin", other); err == nil ||
		!strings.Contains(err.Error(), "owned by plugin") {
		t.Fatalf("foreign plugin must not hijack an owned id, got %v", err)
	}
}

func TestPluginInferenceRequiresExplicitOwnership(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")
	if err := config.WriteInferenceOwned(dir, config.InferenceConfig{
		Instances: []config.Instance{{
			StableID:  "sso-haivivi-main",
			Type:      "deepseek",
			KeySource: config.KeyLiteral,
			KeyValue:  "user-key",
			Enabled:   true,
			Models:    []config.Model{{Name: "deepseek-v4-flash"}},
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	profile := pluginruntime.InferenceProfile{
		ID:     "sso-haivivi-main",
		Type:   "deepseek",
		Models: []pluginruntime.ProfileModel{{Name: "deepseek-v4-flash"}},
		KeyRef: "auth/sso-haivivi/token",
	}
	if err := c.upsertInferenceProfile("sso-haivivi", profile); err == nil ||
		!strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned instance must not be claimed, got %v", err)
	}
}
