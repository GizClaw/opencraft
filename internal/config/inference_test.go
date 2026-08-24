package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func mustProvider(t *testing.T, id string) Provider {
	t.Helper()
	for _, p := range Providers {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("provider %q not in catalog", id)
	return Provider{}
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
		Model:     "gpt-5.6-sol-deploy",
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
		"kind: generate",
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
		Model:     "gpt-5.6-sol-deploy",
		Vision:    true,
		Reasoning: "toggle",
		WebSearch: true,
		Enabled:   true,
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
		Model:     "gpt-5.6-sol-deploy",
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
