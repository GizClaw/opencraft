package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
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
		})
	}
	return cfg
}

func load(t *testing.T, workDir, userDir string) *View {
	t.Helper()
	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
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

	// A router declaration (settings-page output) counts as configured.
	writeFile(t, dir, "opencraft.yaml",
		"resources:\n  router:\n    settings:\n      generate:\n")
	needed, err = InferenceNeeded(dir)
	if err != nil || needed {
		t.Fatalf("with router: needed=%v err=%v, want false", needed, err)
	}

	// Unparseable user layer is treated as unconfigured.
	writeFile(t, dir, "opencraft.yaml", ":: not yaml [")
	needed, err = InferenceNeeded(dir)
	if err != nil || !needed {
		t.Fatalf("broken yaml: needed=%v err=%v, want true", needed, err)
	}
}

func TestInferenceYAMLVariableParts(t *testing.T) {
	cfg := envKeyed(t, "deepseek", "openai")
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	// One deployment resource per enabled instance, with the key
	// profile and the model declaration.
	if !strings.Contains(doc, "api_key: ${env:DEEPSEEK_API_KEY}") {
		t.Fatalf("deepseek profile missing:\n%s", doc)
	}
	if !strings.Contains(doc, "api_key: ${env:OPENAI_API_KEY}") {
		t.Fatalf("openai profile missing:\n%s", doc)
	}
	if strings.Contains(doc, "api_key: ${env:ANTHROPIC_API_KEY}") {
		t.Fatalf("unkeyed anthropic must not carry a profile:\n%s", doc)
	}
	if !strings.Contains(doc, "provider.deepseek-1:") ||
		!strings.Contains(doc, "provider.openai-2:") {
		t.Fatalf("instance deployments missing:\n%s", doc)
	}
	// Router targets = enabled instances in priority order.
	idx := strings.Index(doc, "provider: deepseek-1")
	idx2 := strings.Index(doc, "provider: openai-2")
	if idx < 0 || idx2 < 0 || idx > idx2 {
		t.Fatalf("router priority order wrong:\n%s", doc)
	}
	if strings.Contains(doc, "provider: anthropic-1") {
		t.Fatalf("unkeyed provider must not be a router target:\n%s", doc)
	}
}

func TestInferenceYAMLAzure(t *testing.T) {
	cfg := envKeyed(t, "deepseek")
	cfg.Instances = append(cfg.Instances, Instance{
		Type:      "azure",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models:    []Model{{Name: "gpt-5.6-sol-deploy"}},
		Enabled:   true,
	})
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"provider.azure-2:",
		"endpoint: 'https://res.openai.azure.com'",
		"name: 'gpt-5.6-sol-deploy'",
		"kind: 'generate'",
		"capabilities:",
		"outputs: [text]",
		"provider.azure-2: provider.azure-2", // infer dep merge
		"provider: azure-2",                  // router target
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("azure doc missing %q:\n%s", want, doc)
		}
	}

	// Missing endpoint/model must fail generation.
	bad := envKeyed(t, "deepseek")
	bad.Instances = append(bad.Instances, Instance{Type: "azure", Enabled: true})
	if _, err := bad.InferenceYAML(); err == nil {
		t.Fatal("azure without endpoint must fail")
	}
}

func TestInferenceYAMLAzureCapabilities(t *testing.T) {
	cfg := envKeyed(t, "deepseek")
	cfg.Instances = append(cfg.Instances, Instance{
		Type:      "azure",
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Models: []Model{{
			Name: "gpt-5.6-sol-deploy",
			Capabilities: inference.ModelCapabilities{
				Inputs:          []message.PartKind{message.PartImage},
				Outputs:         []message.PartKind{message.PartText},
				Reasoning:       inference.ReasoningToggle,
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
		"reasoning: 'toggle'",
		"hosted_web_search: true",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("azure capabilities doc missing %q:\n%s", want, doc)
		}
	}

	// Reasoning left off (the empty option) must not emit a reasoning
	// declaration.
	off := envKeyed(t, "deepseek")
	off.Instances = append(off.Instances, Instance{
		Type:      "azure",
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

func TestInferenceYAMLMultipleModels(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "deepseek",
		KeySource: KeyEnv,
		Models: []Model{
			{Name: "deepseek-v4-flash",
				Capabilities: inference.ModelCapabilities{
					HostedWebSearch: true,
				}},
			{Name: "deepseek-v4-pro",
				Capabilities: inference.ModelCapabilities{
					Inputs:    []message.PartKind{message.PartImage},
					Reasoning: inference.ReasoningAlways,
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
		"provider.deepseek-inst-aaa:", // stable resource id
		"name: 'deepseek-v4-flash'",
		"hosted_web_search: true",
		"name: 'deepseek-v4-pro'",
		"reasoning: 'always'",
		"inputs: [image]",
		"provider: deepseek-inst-aaa", // router targets use the same id
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
	if err := WriteInference(dir, cfg); err != nil {
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
	if in.DeploymentID(1) != "deepseek-inst-aaa" {
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
		in.Models[1].Capabilities.Reasoning != inference.ReasoningAlways {
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
			{Name: "img-model", Capabilities: inference.ModelCapabilities{
				Inputs:  []message.PartKind{message.PartText},
				Outputs: []message.PartKind{message.PartImage},
			}},
			{Name: "vid-model", Capabilities: inference.ModelCapabilities{
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
				Capabilities: inference.ModelCapabilities{
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
	if err := WriteInference(dir, cfg); err != nil {
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

func TestInferenceYAMLDeepseekResponsesDerived(t *testing.T) {
	// Responses mode must emit api + per-model responses: true so the
	// deepseek driver accepts declared models.
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "deepseek",
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
	for _, want := range []string{"api: 'responses'", "responses: true"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("deepseek responses doc missing %q:\n%s", want, doc)
		}
	}

	// Chat mode must not derive the responses flag.
	chat := cfg
	chat.Instances = []Instance{{
		StableID: "inst-aaa", Type: "deepseek", KeySource: KeyEnv,
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

func TestLoadInferencePrefersImplOverResourceID(t *testing.T) {
	// The live SSO deployment predates its provider-type migration:
	// the resource id still says openai while impl is deepseek. The
	// parser must trust impl so a later settings save does not regress
	// the driver back to openai.
	dir := t.TempDir()
	writeFile(t, dir, "opencraft.yaml", `
resources:
  provider.openai-sso-haivivi:
    kind: inference.Provider
    impl: deepseek
    settings:
      id: openai-sso-haivivi
      spec:
        api: responses
        models:
          - name: deepseek-v4-flash
            kind: generate
            capabilities:
              outputs: [text]
            responses: true
      profiles:
        - id: sso-haivivi
          secrets:
            api_key: ${secret:keychain.auth/sso-haivivi/token}
  router:
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: openai-sso-haivivi
                  name: deepseek-v4-flash
                profile: sso-haivivi
`)
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	in := cfg.Instances[0]
	if in.Type != "deepseek" {
		t.Fatalf("type = %q, want deepseek (from impl)", in.Type)
	}
	if len(in.Models) != 1 || !in.Models[0].Responses {
		t.Fatalf("models = %+v, want responses: true parsed", in.Models)
	}
	// A settings save must keep impl deepseek and the responses flag.
	if err := WriteInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"impl: deepseek", "api: 'responses'", "responses: true"} {
		if !strings.Contains(string(doc), want) {
			t.Fatalf("rewritten config missing %q:\n%s", want, doc)
		}
	}
}

func TestInferenceYAMLModelNormalization(t *testing.T) {
	// Empty names fall back to the provider default.
	cfg := InferenceConfig{Instances: []Instance{{
		Type: "deepseek", KeySource: KeyEnv, Models: []Model{{Name: ""}}, Enabled: true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: 'deepseek-v4-flash'") {
		t.Fatalf("empty model must default to the provider model:\n%s", data)
	}

	// Duplicate model names are rejected (flowcraft freezes per-provider
	// models by name).
	dup := InferenceConfig{Instances: []Instance{{
		Type: "deepseek", KeySource: KeyEnv,
		Models:  []Model{{Name: "deepseek-v4-flash"}, {Name: " deepseek-v4-flash "}},
		Enabled: true,
	}}}
	if _, err := dup.InferenceYAML(); err == nil {
		t.Fatal("duplicate model names must fail generation")
	}
}

func TestDeploymentIDStableAcrossReorders(t *testing.T) {
	a := Instance{StableID: "inst-a", Type: "deepseek"}
	b := Instance{StableID: "inst-b", Type: "deepseek"}
	if a.DeploymentID(1) != "deepseek-inst-a" || b.DeploymentID(1) != "deepseek-inst-b" {
		t.Fatalf("stable ids must not depend on position: %q %q",
			a.DeploymentID(1), b.DeploymentID(1))
	}
	if a.DeploymentID(9) != "deepseek-inst-a" {
		t.Fatalf("position must not leak into stable ids: %q", a.DeploymentID(9))
	}
	// Legacy rows without a stable id keep the positional form.
	legacy := Instance{Type: "deepseek"}
	if legacy.DeploymentID(3) != "deepseek-3" {
		t.Fatalf("legacy deployment id = %q, want deepseek-3", legacy.DeploymentID(3))
	}
}

func TestLiteralKeyQuoted(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{{
		Type:      "deepseek",
		KeySource: KeyLiteral,
		KeyValue:  "sk-it's-secret",
		Enabled:   true,
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
		Type:      "deepseek",
		KeySource: KeyKeychain,
		KeyValue:  "inference/deepseek-inst-0a1b2c3d",
		Enabled:   true,
	}}}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data),
		"api_key: ${secret:keychain.inference/deepseek-inst-0a1b2c3d}") {
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
		Type:      "deepseek",
		Models:    []Model{{Name: "deepseek-v4-flash"}},
		KeySource: KeyKeychain,
		KeyValue:  "inference/deepseek-inst-0a1b2c3d",
		Enabled:   true,
	}}}
	if err := WriteInference(dir, cfg); err != nil {
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
	if in.KeySource != KeyKeychain || in.KeyValue != "inference/deepseek-inst-0a1b2c3d" {
		t.Fatalf("round trip = (%v, %q), want KeyKeychain + account",
			in.KeySource, in.KeyValue)
	}
}

func TestMatchStoredKeysInheritsKeychainRefs(t *testing.T) {
	existing := []Instance{{
		StableID:  "inst-a",
		Type:      "deepseek",
		KeySource: KeyKeychain,
		KeyValue:  "inference/deepseek-inst-a",
	}}
	rows := []KeyRequest{{
		StableID: "inst-a",
		Type:     "deepseek",
		Models:   []string{"deepseek-v4-flash"},
	}}
	idxs, ok := MatchStoredKeys(existing, rows, map[int]bool{})
	if !ok || len(idxs) != 1 || idxs[0] != 0 {
		t.Fatalf("MatchStoredKeys = (%v, %v), want stable-id match", idxs, ok)
	}
}

func TestInferenceStableIDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-0a1b2c3d",
		Type:      "deepseek",
		Models:    []Model{{Name: "deepseek-v4-flash"}},
		KeySource: KeyLiteral,
		KeyValue:  "sk-roundtrip",
		Enabled:   true,
	}}}
	if err := WriteInference(dir, cfg); err != nil {
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
	cfg := envKeyed(t, "deepseek")
	if err := WriteInference(dir, cfg); err != nil {
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
	if got := DefaultModel(dir); got != "deepseek-1/deepseek-v4-flash" {
		t.Fatalf("DefaultModel = %q", got)
	}

	// Merged view: fixed wiring from the embedded inference layer plus
	// the variable parts from the user layer.
	view := load(t, t.TempDir(), dir)
	if view.Document.Resources["infer"].Kind != "inference.Assembly" {
		t.Fatalf("infer = %+v", view.Document.Resources["infer"])
	}
	for _, id := range []string{"deepseek", "openai", "anthropic", "qwen"} {
		name := "provider." + id
		if _, ok := view.Document.Resources[name]; !ok {
			t.Fatalf("%s missing from merged view", name)
		}
	}
	if _, ok := view.Document.Resources["provider.azure"]; ok {
		t.Fatal("azure must not be registered unconfigured")
	}
	if _, ok := view.Document.Resources["provider.deepseek-1"]; !ok {
		t.Fatal("provider.deepseek-1 missing from merged view")
	}
}

func TestWriteInferenceMultipleModelsLoads(t *testing.T) {
	dir := t.TempDir()
	cfg := InferenceConfig{Instances: []Instance{{
		StableID:  "inst-aaa",
		Type:      "deepseek",
		KeySource: KeyEnv,
		Models: []Model{
			{Name: "deepseek-v4-flash",
				Capabilities: inference.ModelCapabilities{
					HostedWebSearch: true,
				}},
			{Name: "deepseek-v4-pro",
				Capabilities: inference.ModelCapabilities{
					Inputs:    []message.PartKind{message.PartImage},
					Reasoning: inference.ReasoningAlways,
				}},
		},
		Enabled: true,
	}}}
	if err := WriteInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	// The merged config view (strict resource decode) must accept one
	// provider with two models and two router targets sharing the
	// stable profile.
	view := load(t, t.TempDir(), dir)
	if _, ok := view.Document.Resources["provider.deepseek-inst-aaa"]; !ok {
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
	if err := WriteInference(dir, cfg); err != nil {
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
	if got := DefaultModel(dir); got != "openai-1/gpt-5.6-sol" {
		t.Fatalf("DefaultModel = %q, want openai", got)
	}
}

func TestWriteInferenceRejectsNonMappingUserLayer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "opencraft.yaml", "just a scalar\n")
	cfg := envKeyed(t, "deepseek")
	if err := WriteInference(dir, cfg); err == nil {
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
	if err := WriteInference(dir, envKeyed(t, "deepseek")); err != nil {
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
	if !strings.Contains(doc, "provider.deepseek-1: provider.deepseek-1") {
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
	if err := WriteInference(dir, envKeyed(t, "deepseek")); err != nil {
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
	if err := WriteInference(dir, envKeyed(t, "deepseek")); err == nil {
		t.Fatal("WriteInference over a scalar layer must refuse")
	}
}

func TestModelReasoning(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		{StableID: "a", Type: "deepseek", Enabled: true, Models: []Model{
			{Name: "m1", Capabilities: inference.ModelCapabilities{Reasoning: inference.ReasoningToggle}},
			{Name: "m0"}, // no reasoning capability
		}},
		{StableID: "b", Type: "openai", Enabled: true, Models: []Model{
			{Name: "gpt", Capabilities: inference.ModelCapabilities{Reasoning: inference.ReasoningAlways}},
		}},
		{StableID: "c", Type: "qwen", Enabled: false, Models: []Model{
			{Name: "q", Capabilities: inference.ModelCapabilities{Reasoning: inference.ReasoningAlways}},
		}},
	}}
	if !cfg.ModelReasoning("deepseek-a/m1") {
		t.Error("deepseek-a/m1 declares toggle, want true")
	}
	if cfg.ModelReasoning("deepseek-a/m0") {
		t.Error("deepseek-a/m0 has no reasoning capability, want false")
	}
	if !cfg.ModelReasoning("openai-b/gpt") {
		t.Error("openai-b/gpt declares always, want true")
	}
	if cfg.ModelReasoning("qwen-c/q") {
		t.Error("qwen-c is disabled, want false")
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
		{StableID: "a", Type: "deepseek", Enabled: true, Models: []Model{{Name: "m"}}},
	}}
	if plain.ModelReasoning("") {
		t.Error("default target without reasoning capability, want false")
	}
}
