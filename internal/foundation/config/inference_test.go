package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envKeyed(t *testing.T, ids ...string) InferenceConfig {
	t.Helper()
	var cfg InferenceConfig
	for _, id := range ids {
		cfg.Instances = append(cfg.Instances, Instance{
			Type:      id,
			KeySource: KeyEnv,
			Enabled:   true,
			// Opencraft keeps no model table: every instance names the
			// models it serves.
			Models: []Model{{Name: "test-model-" + id}},
		})
	}
	return cfg
}

func load(t *testing.T, workDir, userDir string) *View {
	t.Helper()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestNeeded(t *testing.T) {
	dir := t.TempDir()

	needed, err := InferenceNeeded(dir)
	if err != nil || !needed {
		t.Fatalf("empty dir: needed=%v err=%v, want true", needed, err)
	}

	// A user layer without router targets is unconfigured (the embedded
	// inference layer provides providers/infer/router shell).
	writeFile(t, dir, "opencraft.yaml", "resources:\n  box:\n    impl: local\n")
	needed, err = InferenceNeeded(dir)
	if err != nil || !needed {
		t.Fatalf("no router: needed=%v err=%v, want true", needed, err)
	}

	// A router without targets is still unconfigured: the generated
	// layer always declares the router so the resources that reference
	// it resolve, and an empty target list is the "run setup" state.
	writeFile(t, dir, "opencraft.yaml",
		"resources:\n  router:\n    settings:\n      generate:\n")
	needed, err = InferenceNeeded(dir)
	if err != nil || !needed {
		t.Fatalf("empty router: needed=%v err=%v, want true", needed, err)
	}

	// One generate target marks the install configured.
	writeFile(t, dir, "opencraft.yaml",
		"resources:\n  router:\n    settings:\n      generate:\n"+
			"        - tier: default\n          targets:\n"+
			"            - model:\n                id:\n"+
			"                  provider: openai-1\n                  name: deepseek-v4-flash\n")
	needed, err = InferenceNeeded(dir)
	if err != nil || needed {
		t.Fatalf("with targets: needed=%v err=%v, want false", needed, err)
	}

	// An unparseable user layer is an error, never "unconfigured".
	writeFile(t, dir, "opencraft.yaml", "a: [unterminated\n")
	needed, err = InferenceNeeded(dir)
	if err == nil || needed {
		t.Fatalf("broken yaml: needed=%v err=%v, want error and false", needed, err)
	}
}

func TestRouterConfigured(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want bool
	}{
		{
			name: "no router",
			doc:  "version: v1\nresources: {}\n",
			want: false,
		},
		{
			name: "router shell without targets",
			doc: `version: v1
resources:
  router:
    kind: inference.Router
    impl: unified
    settings:
      retry:
        generate:
          max_attempts: 2
`,
			want: false,
		},
		{
			name: "empty generate pool",
			doc: `version: v1
resources:
  router:
    kind: inference.Router
    impl: unified
    settings:
      generate:
        - tier: default
          targets: []
`,
			want: false,
		},
		{
			name: "generate target",
			doc: `version: v1
resources:
  router:
    kind: inference.Router
    impl: unified
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: openai-1
                  name: deepseek-v4-flash
`,
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := deploy.Parse([]byte(tc.doc))
			if err != nil {
				t.Fatalf("parse doc: %v", err)
			}
			got, err := RouterConfigured(doc)
			if err != nil {
				t.Fatalf("RouterConfigured: %v", err)
			}
			if got != tc.want {
				t.Fatalf("RouterConfigured = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInferenceYAMLVariableParts(t *testing.T) {
	cfg := envKeyed(t, "openai", "anthropic")
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	// One deployment resource per enabled instance, with the key
	// profile and the model declaration.
	if !strings.Contains(doc, "api_key: ${env:OPENAI_API_KEY}") {
		t.Fatalf("openai profile missing:\n%s", doc)
	}
	if strings.Contains(doc, "api_key: ${env:AZURE_OPENAI_API_KEY}") {
		t.Fatalf("no driver defaults to an Azure env var any more:\n%s", doc)
	}
	if !strings.Contains(doc, "provider.openai-1:") ||
		!strings.Contains(doc, "provider.anthropic-2:") {
		t.Fatalf("instance deployments missing:\n%s", doc)
	}
	if !strings.Contains(doc, "request_metadata:\n          envelope: 'client_metadata'") {
		t.Fatalf("client_metadata envelope missing:\n%s", doc)
	}
	if !strings.Contains(doc, "api_key: ${env:ANTHROPIC_API_KEY}") {
		t.Fatalf("anthropic profile missing:\n%s", doc)
	}
	// Router targets = enabled instances in priority order.
	idx := strings.Index(doc, "provider: openai-1")
	idx2 := strings.Index(doc, "provider: anthropic-2")
	if idx < 0 || idx2 < 0 || idx > idx2 {
		t.Fatalf("router priority order wrong:\n%s", doc)
	}
}

func TestInferenceYAMLAzure(t *testing.T) {
	cfg := envKeyed(t, "openai")
	cfg.Instances = append(cfg.Instances, Instance{
		Type:      "openai",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models:    []Model{{Name: "gpt-5.6-sol-deploy"}},
		Advanced: InstanceAdvanced{
			Routing:    "azure_deployment",
			AuthScheme: "header",
			AuthHeader: "api-key",
		},
		Enabled: true,
	})
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"provider.openai-2:",
		"endpoint:\n          base_url: 'https://res.openai.azure.com'",
		"routing: 'azure_deployment'",
		"auth:\n          scheme: 'header'\n          header: 'api-key'",
		"request_metadata:\n          envelope: 'client_metadata'",
		"name: 'gpt-5.6-sol-deploy'",
		"kind: 'generate'",
		"capabilities:",
		"outputs: [text]",
		"provider.openai-2: provider.openai-2", // infer dep merge
		"provider: openai-2",                   // router target
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("azure doc missing %q:\n%s", want, doc)
		}
	}

	// Azure routing without an endpoint must fail generation.
	bad := envKeyed(t, "openai")
	bad.Instances = append(bad.Instances, Instance{
		Type: "openai", Enabled: true,
		Advanced: InstanceAdvanced{Routing: "azure_deployment"},
		Models:   []Model{{Name: "deploy"}},
	})
	if _, err := bad.InferenceYAML(); err == nil {
		t.Fatal("azure without endpoint must fail")
	}
}

// TestInferenceYAMLAdvancedRoundTrip pins the advanced provider knobs
// TestInferenceYAMLNestedDriverFieldWithOneKey pins the encoding of a
// composite driver field whose object carries exactly one key. Deciding
// "scalar or nested" from the marshaled line count inlined it as
// "video: api: v2", which is not a YAML mapping, so the whole user layer
// failed to parse on the next read. MiniMax's video models declare
// exactly this shape.
func TestInferenceYAMLNestedDriverFieldWithOneKey(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-minimax",
		Type:      "minimax",
		KeySource: KeyEnv,
		Enabled:   true,
		Models: []Model{{
			Name: "MiniMax-H3",
			Kind: "video",
			Capabilities: model.ModelCapabilities{
				Outputs: []message.PartKind{message.PartVideo},
			},
			DriverFields: map[string]any{
				"video": map[string]any{"api": "v2"},
			},
		}},
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(data), "            video:\n              api: v2\n",
	) {
		t.Fatalf("composite driver field is not nested:\n%s", data)
	}
	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	video, ok := loaded.Instances[0].Models[0].DriverFields["video"].(map[string]any)
	if !ok || video["api"] != "v2" {
		t.Fatalf(
			"nested driver fields = %+v",
			loaded.Instances[0].Models[0].DriverFields,
		)
	}
}

// TestInferenceYAMLPluginDeclaredProvider covers a provider that is not
// TestInferenceYAMLDriverFactsRoundTrip pins the declaration leaves the
// TestInferenceYAMLDropsRetiredCatalogKey: core v0.4.0 rejects the
// retired `catalog` key. A document that still carries one (our own
// earlier build wrote it for plugin-declared providers) loads, and the
// next write drops it rather than parking it in the opaque provider bag
// where it would keep failing every build.
func TestInferenceYAMLDropsRetiredCatalogKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "opencraft.yaml", `resources:
  provider.openai-inst-aaa:
    kind: inference.Provider
    impl: openai
    settings:
      id: openai-inst-aaa
      spec:
        api: chat
        catalog: declared
        models:
          - name: glm-5.3-flash
            kind: generate
      profiles:
        - id: inst-aaa
          secrets:
            api_key: ${env:OPENAI_API_KEY}
  router:
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: openai-inst-aaa
                  name: glm-5.3-flash
`)
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	// The retired key has no home in the typed configuration, and the
	// rewrite below must drop it rather than keep it where the driver
	// would reject it.
	if err := seedInference(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "catalog") {
		t.Fatalf("retired key survived the rewrite:\n%s", raw)
	}
}

// TestInferenceYAMLDriverFactsRoundTrip pins the declaration leaves the
// settings page edits on top of the basic model row: discovery metadata,
// the driver-specific model fields opencraft does not model, the
// unmodeled provider body fields, and the retention policy's "omit".
func TestInferenceYAMLDriverFactsRoundTrip(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		{
			StableID:  "inst-openai",
			Type:      "openai",
			KeySource: KeyEnv,
			Enabled:   true,
			Advanced: InstanceAdvanced{
				Store:     "omit",
				ExtraBody: map[string]string{"custom_gateway_hint": `"prefer-a"`},
			},
			Models: []Model{{
				Name:         "gpt-5.6-sol",
				Capabilities: model.ModelCapabilities{Outputs: []message.PartKind{message.PartText}},
				Lifecycle: ModelLifecycle{
					Status:              "deprecated",
					ReplacementProvider: "openai",
					ReplacementName:     "gpt-5.6-terra",
					Notes:               "sunset later this year",
				},
			}},
		},
		{
			StableID:  "inst-ark",
			Type:      "bytedance",
			KeySource: KeyEnv,
			Enabled:   false,
			Models: []Model{{
				Name: "doubao-seedance-2-0",
				Kind: "video",
				Capabilities: model.ModelCapabilities{
					Outputs: []message.PartKind{message.PartVideo},
				},
				DriverFields: map[string]any{
					"max_resolution": "1080p",
					"video": map[string]any{
						"seed":                 true,
						"duration_min_seconds": float64(5),
					},
				},
			}},
		},
	}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"store: omit",
		"extra_body:\n            'custom_gateway_hint': \"prefer-a\"",
		"lifecycle:\n              status: 'deprecated'",
		"replacement:\n                provider: 'openai'\n                name: 'gpt-5.6-terra'",
		"notes: 'sunset later this year'",
		"max_resolution: 1080p",
		// The nesting itself is proven by the runtime build test, which
		// makes the driver decode this block.
		"video:",
		"duration_min_seconds: 5",
		"seed: true",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("driver-facts doc missing %q:\n%s", want, doc)
		}
	}

	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Instances) != 2 {
		t.Fatalf("instances = %d, want 2", len(loaded.Instances))
	}
	openai := loaded.Instances[0]
	if openai.Advanced.Store != "omit" ||
		openai.Advanced.ExtraBody["custom_gateway_hint"] != `"prefer-a"` {
		t.Fatalf("advanced round trip = %+v", openai.Advanced)
	}
	lifecycle := openai.Models[0].Lifecycle
	if lifecycle.Status != "deprecated" ||
		lifecycle.ReplacementProvider != "openai" ||
		lifecycle.ReplacementName != "gpt-5.6-terra" ||
		lifecycle.Notes != "sunset later this year" {
		t.Fatalf("lifecycle round trip = %+v", lifecycle)
	}
	ark := loaded.Instances[1]
	if ark.Models[0].DriverFields["max_resolution"] != "1080p" {
		t.Fatalf("driver fields round trip = %+v", ark.Models[0].DriverFields)
	}
	video, ok := ark.Models[0].DriverFields["video"].(map[string]any)
	if !ok || video["seed"] != true {
		t.Fatalf("nested driver fields = %+v", ark.Models[0].DriverFields)
	}
}

// TestInferenceYAMLPluginDeclaredProvider covers a provider that is not
// one of the built-in presets: the instance names its driver, so the
// deployment carries the impl and a declared catalog, and the write/
// read cycle keeps the vendor id intact instead of folding it onto the
// first preset that shares the driver.
func TestInferenceYAMLPluginDeclaredProvider(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "plug-vendor",
		Type:      "vendorx",
		Name:      "Vendor X",
		Driver:    "openai",
		API:       "chat",
		KeySource: KeyKeychain,
		KeyValue:  "auth/plug/vendorx",
		Enabled:   true,
		Endpoint:  "https://api.vendorx.example/v1",
		Models:    []Model{{Name: "vendorx-pro"}},
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"provider.vendorx-plug-vendor:",
		"impl: openai",
		"base_url: 'https://api.vendorx.example/v1'",
		"name: 'vendorx-pro'",
		"provider: vendorx-plug-vendor",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("plugin provider doc missing %q:\n%s", want, doc)
		}
	}

	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(loaded.Instances))
	}
	got := loaded.Instances[0]
	if got.Type != "vendorx" || got.Driver != "openai" ||
		got.StableID != "plug-vendor" {
		t.Fatalf("round-tripped provider = %+v", got)
	}
	if got.DeploymentID(1) != "vendorx-plug-vendor" {
		t.Fatalf("deployment id = %q", got.DeploymentID(1))
	}
}

// TestInferenceYAMLAdvancedRoundTrip pins the advanced provider knobs
// and the router retry policy to the settings page's data model: what
// the form edits must survive a write/read cycle unchanged, including
// the explicit request-metadata opt-out.
func TestInferenceYAMLWireOptionsRoundTrip(t *testing.T) {
	retries, usage := 3, false
	cfg := InferenceConfig{
		Instances: []Instance{{
			StableID:  "inst-aaa",
			Type:      "openai",
			API:       "chat",
			KeySource: KeyEnv,
			Enabled:   true,
			Endpoint:  "https://gateway.example/v1",
			Advanced: InstanceAdvanced{
				Routing:          "azure_deployment",
				Query:            map[string]string{"api-version": "2025-04-01-preview"},
				Headers:          map[string]string{"x-gateway": "one"},
				Organization:     "org-1",
				Project:          "proj-2",
				Timeout:          "90s",
				AuthScheme:       "header",
				AuthHeader:       "x-api-key",
				MetadataEnvelope: "-",
				HTTPRetries:      &retries,
				Store:            "false",
				ReasoningChannel: "text",
				ChatIncludeUsage: &usage,
			},
			Models: []Model{{Name: "glm-5.3-flash"}},
		}},
		Router: RouterPolicy{MaxAttempts: 4, FallbackOnRetryExhausted: false},
	}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"endpoint:\n          base_url: 'https://gateway.example/v1'",
		"routing: 'azure_deployment'",
		"organization: 'org-1'",
		"project: 'proj-2'",
		"timeout: '90s'",
		"headers:\n            'x-gateway': 'one'",
		"query:\n            'api-version': '2025-04-01-preview'",
		"auth:\n          scheme: 'header'\n          header: 'x-api-key'",
		"request_metadata: {}",
		"http_retries: 3",
		"store: false",
		"reasoning_channel: 'text'",
		"chat_stream_options:\n            include_usage: false",
		"max_attempts: 4",
		"fallback_on_retry_exhausted: false",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("advanced doc missing %q:\n%s", want, doc)
		}
	}

	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(loaded.Instances))
	}
	got := loaded.Instances[0]
	if got.Endpoint != "https://gateway.example/v1" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	adv := got.Advanced
	if adv.Routing != "azure_deployment" ||
		adv.Organization != "org-1" ||
		adv.Project != "proj-2" ||
		adv.Timeout != "90s" ||
		adv.AuthScheme != "header" ||
		adv.AuthHeader != "x-api-key" ||
		adv.MetadataEnvelope != "-" ||
		adv.ReasoningChannel != "text" ||
		adv.HTTPRetries == nil || *adv.HTTPRetries != 3 ||
		adv.Store != "false" ||
		adv.ChatIncludeUsage == nil || *adv.ChatIncludeUsage != false {
		t.Fatalf("advanced round trip = %+v", adv)
	}
	if adv.Query["api-version"] != "2025-04-01-preview" ||
		adv.Headers["x-gateway"] != "one" {
		t.Fatalf("advanced maps = %+v / %+v", adv.Query, adv.Headers)
	}
	if loaded.Router.MaxAttempts != 4 ||
		loaded.Router.FallbackOnRetryExhausted {
		t.Fatalf("router policy = %+v", loaded.Router)
	}
}

func TestInferenceYAMLAzureCapabilities(t *testing.T) {
	cfg := envKeyed(t, "openai")
	cfg.Instances = append(cfg.Instances, Instance{
		Type:      "openai",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models: []Model{{
			Name: "gpt-5.6-sol-deploy",
			Capabilities: model.ModelCapabilities{
				Inputs:          []message.PartKind{message.PartImage},
				Outputs:         []message.PartKind{message.PartText},
				Reasoning:       model.ReasoningCapability{Kind: model.ReasoningToggle},
				HostedWebSearch: true,
			},
		}},
		Enabled: true,
	})
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"outputs: [text]",
		"inputs: [image]",
		"reasoning:\n                kind: 'toggle'",
		"hosted_web_search: true",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("azure capabilities doc missing %q:\n%s", want, doc)
		}
	}

	// Reasoning left off (the empty option) must not emit a reasoning
	// declaration.
	off := envKeyed(t, "openai")
	off.Instances = append(off.Instances, Instance{
		Type:      "openai",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models:    []Model{{Name: "gpt-5.6-sol-deploy"}},
		Enabled:   true,
	})
	data, err = off.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "reasoning:") {
		t.Fatalf("default azure must not declare reasoning:\n%s", data)
	}
}

func TestInferenceYAMLLimitsRoundTrip(t *testing.T) {
	input, output := 1_000_000, 65_536
	cfg := envKeyed(t, "openai")
	cfg.Instances = append(cfg.Instances, Instance{
		Type:      "openai",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models: []Model{{
			Name: "gpt-5.6-sol-deploy",
			Limits: model.ModelLimits{
				MaxInputTokens:  &input,
				MaxOutputTokens: &output,
			},
		}},
		Enabled: true,
	})
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"limits:",
		"max_input_tokens: 1000000",
		"max_output_tokens: 65536",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("limits doc missing %q:\n%s", want, doc)
		}
	}

	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got *model.ModelLimits
	for _, in := range loaded.Instances {
		if in.Type != "openai" || len(in.Models) != 1 {
			continue
		}
		limits := in.Models[0].Limits
		got = &limits
	}
	if got == nil || got.MaxInputTokens == nil ||
		*got.MaxInputTokens != input ||
		got.MaxOutputTokens == nil || *got.MaxOutputTokens != output {
		t.Fatalf("round-tripped limits = %+v, want %d/%d", got, input, output)
	}

	// A model that declares no limits stays silent: the driver then
	// publishes none for it rather than inheriting a vendor default.
	plain := envKeyed(t, "openai")
	plain.Instances = append(plain.Instances, Instance{
		Type:      "openai",
		KeySource: KeyEnv,
		Models:    []Model{{Name: "deepseek-v4-flash"}},
		Enabled:   true,
	})
	data, err = plain.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "limits:") {
		t.Fatalf("default model must not declare limits:\n%s", data)
	}

	// Non-positive declared limits must fail generation, mirroring the
	// driver-side ModelLimits validation.
	zero := 0
	bad := envKeyed(t, "openai")
	bad.Instances = append(bad.Instances, Instance{
		Type:      "openai",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models: []Model{{
			Name:   "gpt-5.6-sol-deploy",
			Limits: model.ModelLimits{MaxInputTokens: &zero},
		}},
		Enabled: true,
	})
	if _, err := bad.InferenceYAML(); err == nil {
		t.Fatal("non-positive max input tokens unexpectedly accepted")
	}
}

func TestInferenceYAMLReasoningEffortMap(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "openai",
		KeySource: KeyEnv,
		Models: []Model{{
			Name: "deepseek-v4-pro",
			Capabilities: model.ModelCapabilities{
				Reasoning: model.ReasoningCapability{
					Kind: model.ReasoningToggle,
					EffortMap: map[model.ReasoningEffort]string{
						model.ReasoningMinimal: "low",
						model.ReasoningLow:     "low",
						model.ReasoningMedium:  "high",
						model.ReasoningHigh:    "high",
						model.ReasoningXHigh:   "max",
					},
				},
			},
		}},
		Enabled: true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"reasoning:",
		"kind: 'toggle'",
		"effort_map:",
		"'minimal': 'low'",
		"'medium': 'high'",
		"'xhigh': 'max'",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("effort map doc missing %q:\n%s", want, doc)
		}
	}

	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Instances) != 1 || len(loaded.Instances[0].Models) != 1 {
		t.Fatalf("round trip = %+v", loaded.Instances)
	}
	got := loaded.Instances[0].Models[0].Capabilities.Reasoning
	if got.Kind != model.ReasoningToggle {
		t.Fatalf("round trip reasoning kind = %q, want toggle", got.Kind)
	}
	if got.EffortMap[model.ReasoningXHigh] != "max" ||
		got.EffortMap[model.ReasoningMedium] != "high" {
		t.Fatalf("round trip effort map = %+v", got.EffortMap)
	}
}

func TestInferenceYAMLMultipleModels(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "openai",
		KeySource: KeyEnv,
		Models: []Model{
			{Name: "deepseek-v4-flash",
				Capabilities: model.ModelCapabilities{
					HostedWebSearch: true,
				}},
			{Name: "deepseek-v4-pro",
				Capabilities: model.ModelCapabilities{
					Inputs:    []message.PartKind{message.PartImage},
					Reasoning: model.ReasoningCapability{Kind: model.ReasoningAlways},
				}},
		},
		Enabled: true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"provider.openai-inst-aaa:", // stable resource id
		"name: 'deepseek-v4-flash'",
		"hosted_web_search: true",
		"name: 'deepseek-v4-pro'",
		"reasoning:\n                kind: 'always'",
		"inputs: [image]",
		"provider: openai-inst-aaa", // router targets use the same id
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("multi-model doc missing %q:\n%s", want, doc)
		}
	}
	if got := strings.Count(doc, "- model:"); got != 2 {
		t.Fatalf("router must declare one target per model, got %d:\n%s", got, doc)
	}

	// Round trip: both models and their capabilities survive.
	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Instances) != 1 {
		t.Fatalf("round trip instances = %d, want 1", len(got.Instances))
	}
	in := got.Instances[0]
	if in.DeploymentID(1) != "openai-inst-aaa" {
		t.Fatalf("round trip deployment id = %q", in.DeploymentID(1))
	}
	if len(in.Models) != 2 {
		t.Fatalf("round trip models = %+v, want 2", in.Models)
	}
	if in.Models[0].Name != "deepseek-v4-flash" ||
		!in.Models[0].Capabilities.HostedWebSearch {
		t.Fatalf("model 0 = %+v", in.Models[0])
	}
	if in.Models[1].Name != "deepseek-v4-pro" ||
		len(in.Models[1].Capabilities.Inputs) != 1 ||
		in.Models[1].Capabilities.Inputs[0] != message.PartImage ||
		in.Models[1].Capabilities.Reasoning.Kind != model.ReasoningAlways {
		t.Fatalf("model 1 = %+v", in.Models[1])
	}
}

func TestInferenceYAMLGenerationKinds(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "bytedance",
		KeySource: KeyEnv,
		Models: []Model{
			{Name: "text-model"},
			{Name: "img-model", Capabilities: model.ModelCapabilities{
				Inputs:  []message.PartKind{message.PartText},
				Outputs: []message.PartKind{message.PartImage},
			}},
			{Name: "vid-model", Capabilities: model.ModelCapabilities{
				Inputs:  []message.PartKind{message.PartText, message.PartImage},
				Outputs: []message.PartKind{message.PartVideo},
			}},
		},
		Enabled: true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"name: 'text-model'",
		"kind: 'generate'",
		"outputs: [text]",
		"inputs: [text]",
		"name: 'img-model'",
		"kind: 'image'",
		"outputs: [image]",
		"inputs: [text]",
		"name: 'vid-model'",
		"kind: 'video'",
		"outputs: [video]",
		"inputs: [text, image]",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("generation kinds doc missing %q:\n%s", want, doc)
		}
	}
}

func TestInferenceYAMLByTedanceEndpoints(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "bytedance",
		KeySource: KeyEnv,
		Models: []Model{
			{Name: "doubao-seedance-1-6-pro", Endpoint: "ep-20260801-abc",
				Capabilities: model.ModelCapabilities{
					Inputs:  []message.PartKind{message.PartText},
					Outputs: []message.PartKind{message.PartVideo},
				}},
			{Name: "doubao-seed-2-0-lite-260428"},
		},
		Enabled: true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"endpoints:",
		"'doubao-seedance-1-6-pro': 'ep-20260801-abc'",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("bytedance endpoints doc missing %q:\n%s", want, doc)
		}
	}

	// Round trip: the endpoint rides per model and the unbound model
	// stays unbound.
	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	models := got.Instances[0].Models
	if len(models) != 2 || models[0].Endpoint != "ep-20260801-abc" ||
		models[1].Endpoint != "" {
		t.Fatalf("bytedance endpoints round trip = %+v", models)
	}
}

func TestInferenceYAMLDeepseekResponsesSurface(t *testing.T) {
	// The responses surface is provider-level: api: responses is
	// written and per-model responses flags are gone (the driver now
	// serves every generate model on that surface).
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "openai",
		KeySource: KeyEnv,
		API:       "responses",
		Models:    []Model{{Name: "deepseek-v4-flash"}},
		Enabled:   true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, "api: 'responses'") {
		t.Fatalf("deepseek responses doc missing api surface:\n%s", doc)
	}
	if strings.Contains(doc, "responses:") {
		t.Fatalf("deepseek responses mode must not declare per-model responses:\n%s", doc)
	}

	// Chat mode must not emit the flag either.
	chat := cfg
	chat.Instances = []Instance{{
		StableID: "inst-aaa", Type: "openai", KeySource: KeyEnv,
		API: "chat", Models: []Model{{Name: "deepseek-v4-flash"}}, Enabled: true,
	}}
	data, err = chat.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "responses:") {
		t.Fatalf("chat mode must not declare responses:\n%s", data)
	}
}

func TestMigrateUserInferenceConfigDropsDeprecatedKeys(t *testing.T) {
	cfg := envKeyed(t, "openai")
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(
		string(data),
		"kind: 'generate'",
		"kind: 'generate'\n"+
			"            effort_none: true\n"+
			"            responses: true\n"+
			"            dimensions: true",
		1,
	)
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "opencraft.yaml"),
		[]byte(stale),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	changed, err := MigrateUserInferenceConfig(dir)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatal("stale document must report a migration")
	}
	doc, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"effort_none", "responses: true", "dimensions"} {
		if strings.Contains(string(doc), key) {
			t.Fatalf("migrated doc still contains %q:\n%s", key, doc)
		}
	}
	if _, err := LoadInference(dir); err != nil {
		t.Fatalf("migrated config must load: %v", err)
	}

	changed, err = MigrateUserInferenceConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("clean document must not rewrite")
	}

	if _, err := MigrateUserInferenceConfig(t.TempDir()); err != nil {
		t.Fatalf("missing document must be a no-op: %v", err)
	}
}

func TestInferenceYAMLAdvancedRoundTrip(t *testing.T) {
	includeUsage := false
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "openai",
		KeySource: KeyEnv,
		API:       "chat",
		Advanced: InstanceAdvanced{
			ChatIncludeUsage: &includeUsage,
		},
		Models:  []Model{{Name: "glm-5.3-flash"}},
		Enabled: true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"api: 'chat'",
		"wire:",
		"chat_stream_options:\n            include_usage: false",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("provider spec doc missing %q:\n%s", want, doc)
		}
	}

	dir := t.TempDir()
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Instances) != 1 {
		t.Fatalf("round trip instances = %+v", loaded.Instances)
	}
	got := loaded.Instances[0].Advanced.ChatIncludeUsage
	if got == nil || *got {
		t.Fatalf("round trip chat options = %v, want false",
			loaded.Instances[0].Advanced)
	}

	// An unset knob must not emit a chat_stream_options section.
	plain := cfg
	plain.Instances[0].Advanced.ChatIncludeUsage = nil
	data, err = plain.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "chat_stream_options:") {
		t.Fatalf("nil provider spec must not be emitted:\n%s", data)
	}
}

// TestInferenceYAMLFoldsLegacyChatOptions pins the compat read: a
// document written before the chat stream options moved under wire
// still feeds the typed knobs, and the next write emits the current
// placement.
func TestInferenceYAMLFoldsLegacyChatOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencraft.yaml")
	legacy := `version: v1
resources:
  provider.openai-inst-aaa:
    kind: inference.Provider
    impl: openai
    settings:
      id: openai-inst-aaa
      spec:
        api: chat
        chat_stream_options:
          include_usage: false
        models:
          - name: glm-5.3-flash
            kind: generate
      profiles:
        - id: inst-aaa
          secrets:
            api_key: ${env:OPENAI_API_KEY}
  router:
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: openai-inst-aaa
                  name: glm-5.3-flash
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	usage := cfg.Instances[0].Advanced.ChatIncludeUsage
	if usage == nil || *usage {
		t.Fatalf("legacy chat options not folded: %+v", cfg.Instances[0].Advanced)
	}

	if err := seedInference(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "wire:") ||
		strings.Contains(string(raw), "\n        chat_stream_options:") {
		t.Fatalf("rewrite must place chat options under wire:\n%s", raw)
	}
}

func TestInferenceYAMLModelNormalization(t *testing.T) {
	// Opencraft keeps no model table: a nameless model row is a
	// validation error, not a silent default, and a nameless model list
	// becomes one nameless row that fails the same way.
	unnamed := InferenceConfig{Instances: []Instance{{
		Type: "openai", KeySource: KeyEnv, Models: []Model{{Name: ""}}, Enabled: true,
	}}}
	if _, err := unnamed.InferenceYAML(); err == nil ||
		!strings.Contains(err.Error(), "model name is required") {
		t.Fatalf("unnamed model = %v, want a required-name error", err)
	}
	empty := InferenceConfig{Instances: []Instance{{
		Type: "openai", KeySource: KeyEnv, Enabled: true,
	}}}
	if _, err := empty.InferenceYAML(); err == nil ||
		!strings.Contains(err.Error(), "model name is required") {
		t.Fatalf("empty model list = %v, want a required-name error", err)
	}

	// Names are trimmed and duplicates are rejected (flowcraft freezes
	// per-provider models by name).
	dup := InferenceConfig{Instances: []Instance{{
		Type: "openai", KeySource: KeyEnv,
		Models:  []Model{{Name: "glm-5.3-flash"}, {Name: " glm-5.3-flash "}},
		Enabled: true,
	}}}
	if _, err := dup.InferenceYAML(); err == nil {
		t.Fatal("duplicate model names must fail generation")
	}
}

func TestDeploymentIDStableAcrossReorders(t *testing.T) {
	a := Instance{StableID: "inst-a", Type: "openai"}
	b := Instance{StableID: "inst-b", Type: "openai"}
	if a.DeploymentID(1) != "openai-inst-a" || b.DeploymentID(1) != "openai-inst-b" {
		t.Fatalf("stable ids must not depend on position: %q %q",
			a.DeploymentID(1), b.DeploymentID(1))
	}
	if a.DeploymentID(9) != "openai-inst-a" {
		t.Fatalf("position must not leak into stable ids: %q", a.DeploymentID(9))
	}
	// Instances without a stable id use the positional form.
	positional := Instance{Type: "openai"}
	if positional.DeploymentID(3) != "openai-3" {
		t.Fatalf("positional deployment id = %q, want openai-3", positional.DeploymentID(3))
	}
}

func TestLiteralKeyQuoted(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		Type:      "openai",
		KeySource: KeyLiteral,
		KeyValue:  "sk-it's-secret",
		Enabled:   true,
		Models:    []Model{{Name: "glm-5.3-flash"}},
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "api_key: 'sk-it''s-secret'") {
		t.Fatalf("literal key not quoted:\n%s", data)
	}
}

func TestKeychainKeyRenderedAsSecretRef(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-0a1b2c3d",
		Type:      "openai",
		KeySource: KeyKeychain,
		KeyValue:  "inference/openai-inst-0a1b2c3d",
		Enabled:   true,
		Models:    []Model{{Name: "glm-5.3-flash"}},
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data),
		"api_key: ${secret:keychain.inference/openai-inst-0a1b2c3d}") {
		t.Fatalf("keychain key not rendered as secret ref:\n%s", data)
	}
	// The plaintext must never appear in the config.
	if strings.Contains(string(data), "sk-") {
		t.Fatalf("config leaked a secret:\n%s", data)
	}
}

func TestKeychainKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-0a1b2c3d",
		Type:      "openai",
		Models:    []Model{{Name: "deepseek-v4-flash"}},
		KeySource: KeyKeychain,
		KeyValue:  "inference/openai-inst-0a1b2c3d",
		Enabled:   true,
	}}}
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(got.Instances))
	}
	in := got.Instances[0]
	if in.KeySource != KeyKeychain || in.KeyValue != "inference/openai-inst-0a1b2c3d" {
		t.Fatalf("round trip = (%v, %q), want KeyKeychain + account",
			in.KeySource, in.KeyValue)
	}
}

// TestSettingsSaveCarriesStoredCredential pins the settings-page carry
// over: a row that does not restate its credential keeps the stored one
// (matched by stable identity), while a brand-new enabled row cannot
// inherit a credential it never had.
func TestSettingsSaveCarriesStoredCredential(t *testing.T) {
	dir := t.TempDir()
	if err := seedInference(dir, InferenceConfig{Instances: []Instance{{
		StableID:  "inst-a",
		Type:      "openai",
		Name:      "gateway",
		API:       "responses",
		KeySource: KeyKeychain,
		KeyValue:  "inference/openai-inst-a",
		Enabled:   true,
		Models:    []Model{{Name: "deepseek-v4-flash"}},
	}}}); err != nil {
		t.Fatal(err)
	}

	// The page swaps the row's model without restating the credential.
	edited := InstanceSpec{
		StableID: "inst-a",
		Type:     "openai",
		API:      "responses",
		Models:   []ModelSpec{{Name: "deepseek-v4-pro"}},
		Enabled:  boolPtr(true),
	}
	if _, err := ApplySettingsSave(dir, SaveRequest{
		Instances: []InstanceSpec{edited},
	}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	in := cfg.Instances[0]
	if in.Models[0].Name != "deepseek-v4-pro" {
		t.Fatalf("edit lost: %+v", in)
	}
	if in.KeySource != KeyKeychain || in.KeyValue != "inference/openai-inst-a" {
		t.Fatalf("stored credential lost: (%v, %q)", in.KeySource, in.KeyValue)
	}

	// A new enabled row stating no credential is refused instead of
	// silently pinning the provider's environment variable.
	fresh := InstanceSpec{
		Type:    "openai",
		API:     "responses",
		Models:  []ModelSpec{{Name: "gpt-5.6-sol"}},
		Enabled: boolPtr(true),
	}
	if _, err := ApplySettingsSave(dir, SaveRequest{
		Instances: []InstanceSpec{fresh},
	}, nil); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("keyless new row accepted: %v", err)
	}
}

func TestInferenceStableIDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-0a1b2c3d",
		Type:      "openai",
		Models:    []Model{{Name: "deepseek-v4-flash"}},
		KeySource: KeyLiteral,
		KeyValue:  "sk-roundtrip",
		Enabled:   true,
	}}}
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "stable_id") {
		t.Fatalf("provider settings must not carry a stable_id key (flowcraft rejects it):\n%s", data)
	}
	if !strings.Contains(string(data), "id: 'inst-0a1b2c3d'") {
		t.Fatalf("stable identity must ride in the profile id:\n%s", data)
	}
	if !strings.Contains(string(data), "profile: 'inst-0a1b2c3d'") {
		t.Fatalf("router target must select the stable profile:\n%s", data)
	}

	got, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Instances) != 1 || got.Instances[0].StableID != "inst-0a1b2c3d" {
		t.Fatalf("round trip lost stable id: %+v", got.Instances)
	}
	if got.Instances[0].KeyValue != "sk-roundtrip" {
		t.Fatalf("round trip lost key: %+v", got.Instances[0])
	}
}

func TestWriteAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := envKeyed(t, "openai")
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "opencraft.yaml")
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("opencraft.yaml mode = %o, want 0600", st.Mode().Perm())
	}

	needed, err := InferenceNeeded(dir)
	if err != nil || needed {
		t.Fatalf("after write: needed=%v err=%v", needed, err)
	}
	if got := DefaultModel(dir); got != "openai-1/test-model-openai" {
		t.Fatalf("DefaultModel = %q", got)
	}

	// Merged view: fixed wiring from the embedded inference layer plus
	// the variable parts from the user layer.
	view := load(t, t.TempDir(), dir)
	if view.Document.Resources["infer"].Kind != "inference.Assembly" {
		t.Fatalf("infer = %+v", view.Document.Resources["infer"])
	}
	// Every provider resource comes from the user layer: a wire-family
	// driver carries no vendor facts, so the embedded layer declares
	// only the assembly and the router shell.
	if _, ok := view.Document.Resources["provider.azure"]; ok {
		t.Fatal("azure must not be registered unconfigured")
	}
	if _, ok := view.Document.Resources["provider.openai-1"]; !ok {
		t.Fatal("provider.openai-1 missing from merged view")
	}
}

func TestWriteInferenceMultipleModelsLoads(t *testing.T) {
	dir := t.TempDir()
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "openai",
		KeySource: KeyEnv,
		Models: []Model{
			{Name: "deepseek-v4-flash",
				Capabilities: model.ModelCapabilities{
					HostedWebSearch: true,
				}},
			{Name: "deepseek-v4-pro",
				Capabilities: model.ModelCapabilities{
					Inputs:    []message.PartKind{message.PartImage},
					Reasoning: model.ReasoningCapability{Kind: model.ReasoningAlways},
				}},
		},
		Enabled: true,
	}}}
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	// The merged config view (strict resource decode) must accept one
	// provider with two models and two router targets sharing the
	// stable profile.
	view := load(t, t.TempDir(), dir)
	if _, ok := view.Document.Resources["provider.openai-inst-aaa"]; !ok {
		t.Fatal("stable provider resource missing from merged view")
	}
}

func TestWriteInferencePreservesManualResources(t *testing.T) {
	dir := t.TempDir()
	// A user layer that was hand-edited: an MCP source plus a custom
	// agents section, on top of a previously generated inference block.
	existing := `version: v1
resources:
  router:
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: deepseek
                  name: deepseek-v4-flash
  provider.deepseek:
    settings:
      profiles:
        - secrets:
            api_key: ${env:DEEPSEEK_API_KEY}
  tool.mcp:
    kind: tool.Source
    impl: mcp
    settings:
      servers:
        - name: my-server
          transport: stdio
          command: my-mcp-server
  tools:
    deps:
      tool.mcp: tool.mcp
agents:
  assistant:
    engine:
      settings:
        graph: {file: graphs/my-assistant.yaml}
`
	writeFile(t, dir, "opencraft.yaml", existing)

	// Re-save a different inference selection: openai now first,
	// deepseek removed.
	cfg := envKeyed(t, "openai")
	if err := seedInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	// Managed resources were replaced by the new selection.
	if strings.Contains(doc, "provider.deepseek:") {
		t.Fatalf("removed provider still present:\n%s", doc)
	}
	if !strings.Contains(doc, "provider: openai-1") {
		t.Fatalf("new provider missing:\n%s", doc)
	}
	// Manual resources survived verbatim.
	for _, want := range []string{
		"tool.mcp:",
		"command: my-mcp-server",
		"tool.mcp: tool.mcp",
		"graphs/my-assistant.yaml",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("manual resource missing %q:\n%s", want, doc)
		}
	}

	// The merged view still carries the manual resources and routes
	// through the saved selection.
	view := load(t, t.TempDir(), dir)
	if _, ok := view.Document.Resources["tool.mcp"]; !ok {
		t.Fatal("tool.mcp missing from merged view")
	}
	if got := DefaultModel(dir); got != "openai-1/test-model-openai" {
		t.Fatalf("DefaultModel = %q, want openai", got)
	}
}

func TestWriteInferenceRejectsNonMappingUserLayer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "opencraft.yaml", "just a scalar\n")
	cfg := envKeyed(t, "openai")
	if err := seedInference(dir, cfg); err == nil {
		t.Fatal("WriteInference over a non-mapping user layer must fail, not silently clobber it")
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "just a scalar\n" {
		t.Fatalf("non-mapping user layer was clobbered:\n%s", data)
	}
}

func TestWriteInferenceRemovesStaleAzureInferDep(t *testing.T) {
	dir := t.TempDir()
	existing := `version: v1
resources:
  provider.azure:
    kind: inference.Provider
    impl: azure
    settings:
      id: azure
  infer:
    deps:
      provider.azure: provider.azure
  router:
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: azure
                  name: deployment
`
	writeFile(t, dir, "opencraft.yaml", existing)
	if err := seedInference(dir, envKeyed(t, "openai")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if strings.Contains(doc, "provider.azure") {
		t.Fatalf("stale azure provider survived a non-azure re-save:\n%s", doc)
	}
	if !strings.Contains(doc, "provider.openai-1: provider.openai-1") {
		t.Fatalf("new instance infer dep missing:\n%s", doc)
	}
}

// TestLoadInferenceMissingConfig verifies first launch with no user
// configuration layer does not fail: LoadInference must return an empty
// config (no instances), so the desktop init flow can proceed to the
// "inference not configured" guide.
func TestLoadInferenceMissingConfig(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatalf("LoadInference(missing config) = %v, want nil", err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("LoadInference(missing config) instances = %d, want 0", len(cfg.Instances))
	}
}

// TestWriteInferenceOverwritesEmptyUserLayer verifies first-time setup
// is not blocked by an empty opencraft.yaml (e.g. created with touch):
// an empty layer has no data to preserve and is replaced by the fresh
// document.
func TestWriteInferenceOverwritesEmptyUserLayer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "opencraft.yaml", "")
	if err := seedInference(dir, envKeyed(t, "openai")); err != nil {
		t.Fatalf("WriteInference over empty layer: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "resources:") {
		t.Fatalf("user layer not written:\n%s", data)
	}
}

// TestWriteInferenceRefusesNonMappingLayer keeps the guard for a
// non-empty scalar layer that would otherwise destroy user data.
func TestWriteInferenceRefusesNonMappingLayer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "opencraft.yaml", "just some text\n")
	if err := seedInference(dir, envKeyed(t, "openai")); err == nil {
		t.Fatal("WriteInference over a scalar layer must refuse")
	}
}

// TestModelServesText pins the chat eligibility rule shared by the
// model picker and the default reasoning-model resolution: an
// undeclared output list stays eligible because the router treats it as
// compatible, a declared list must contain text, and the
// generation-only families are tool targets.
func TestModelServesText(t *testing.T) {
	tests := []struct {
		name    string
		outputs []message.PartKind
		want    bool
	}{
		{name: "undeclared", want: true},
		{name: "text", outputs: []message.PartKind{message.PartText}, want: true},
		{name: "text and image", outputs: []message.PartKind{
			message.PartText, message.PartImage,
		}, want: true},
		{name: "image", outputs: []message.PartKind{message.PartImage}, want: false},
		{name: "video", outputs: []message.PartKind{message.PartVideo}, want: false},
		{name: "audio", outputs: []message.PartKind{message.PartAudio}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{Capabilities: model.ModelCapabilities{Outputs: tt.outputs}}
			if got := m.ServesText(); got != tt.want {
				t.Fatalf("ServesText() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestModelReasoning(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		{StableID: "a", Type: "openai", Enabled: true, Models: []Model{
			{Name: "m1", Capabilities: model.ModelCapabilities{Reasoning: model.ReasoningCapability{Kind: model.ReasoningToggle}}},
			{Name: "m0"}, // no reasoning capability
		}},
		{StableID: "b", Type: "openai", Enabled: true, Models: []Model{
			{Name: "gpt", Capabilities: model.ModelCapabilities{Reasoning: model.ReasoningCapability{Kind: model.ReasoningAlways}}},
		}},
		{StableID: "c", Type: "openai", Enabled: false, Models: []Model{
			{Name: "q", Capabilities: model.ModelCapabilities{Reasoning: model.ReasoningCapability{Kind: model.ReasoningAlways}}},
		}},
	}}
	if !cfg.ModelReasoning("openai-a/m1") {
		t.Error("openai-a/m1 declares toggle, want true")
	}
	if cfg.ModelReasoning("openai-a/m0") {
		t.Error("openai-a/m0 has no reasoning capability, want false")
	}
	if !cfg.ModelReasoning("openai-b/gpt") {
		t.Error("openai-b/gpt declares always, want true")
	}
	if cfg.ModelReasoning("openai-c/q") {
		t.Error("openai-c is disabled, want false")
	}
	if cfg.ModelReasoning("unknown/x") {
		t.Error("unknown model, want false")
	}
	// Empty hint resolves to the default target (first enabled
	// instance, first model): deepseek-a/m1 with toggle.
	if !cfg.ModelReasoning("") {
		t.Error("empty hint should resolve to deepseek-a/m1 (toggle)")
	}

	plain := InferenceConfig{Instances: []Instance{
		{StableID: "a", Type: "openai", Enabled: true, Models: []Model{{Name: "m"}}},
	}}
	if plain.ModelReasoning("") {
		t.Error("default target without reasoning capability, want false")
	}

	// The default text target skips generation-only rows: an image model
	// listed first must not decide the reasoning knob for the text model
	// the router would actually run.
	mixed := InferenceConfig{Instances: []Instance{
		{StableID: "m", Type: "openai", Enabled: true, Models: []Model{
			{Name: "gpt-image-2", Capabilities: model.ModelCapabilities{
				Outputs: []message.PartKind{message.PartImage},
			}},
			{Name: "gpt-5", Capabilities: model.ModelCapabilities{
				Outputs: []message.PartKind{message.PartText},
				Reasoning: model.ReasoningCapability{
					Kind: model.ReasoningAlways,
				},
			}},
		}},
	}}
	if !mixed.ModelReasoning("") {
		t.Error("empty hint should skip the image row and resolve to the text model")
	}
}
