package config

import (
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
)

// webSearchInstance builds one enabled instance whose models all
// declare the given hosted web search capability.
func webSearchInstance(
	typ, stableID, api string,
	hosted ...bool,
) Instance {
	in := Instance{
		StableID:  stableID,
		Type:      typ,
		API:       api,
		KeySource: KeyEnv,
		Enabled:   true,
	}
	for i, on := range hosted {
		name := "model"
		if i > 0 {
			name += string(rune('0' + i))
		}
		in.Models = append(in.Models, Model{
			Name: name,
			Capabilities: inference.ModelCapabilities{
				HostedWebSearch: on,
			},
		})
	}
	return in
}

func TestWebSearchExtensionsDeepSeekResponses(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("deepseek", "inst-aaa", "responses", true),
	}}
	out := cfg.WebSearchExtensions()
	if len(out) != 1 {
		t.Fatalf("extensions = %+v, want one", out)
	}
	got := out[0]
	if got.Provider != "deepseek-inst-aaa" ||
		got.ID != "generate_options" {
		t.Fatalf("entry = %+v", got)
	}
	ws, ok := got.Fields["web_search"].(map[string]any)
	if !ok {
		t.Fatalf("fields = %+v, want web_search knob", got.Fields)
	}
	tc, ok := ws["tool_choice"].(map[string]any)
	if !ok || tc["required"] != false {
		t.Fatalf("web_search fields = %+v, want tool_choice auto", ws)
	}
}

func TestWebSearchExtensionsSkipsChatSurface(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("openai", "inst-aaa", "chat", true),
	}}
	if out := cfg.WebSearchExtensions(); len(out) != 0 {
		t.Fatalf("chat instance emitted extensions: %+v", out)
	}
}

func TestWebSearchExtensionsSkipsMixedGenerateModels(t *testing.T) {
	// One deployment mixes a searchable and a non-searchable generate
	// model. Provider-level extensions cannot express per-model
	// enablement, so the deployment is skipped wholesale rather than
	// breaking the non-searchable model at compile time.
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("deepseek", "inst-aaa", "responses", true, false),
	}}
	if out := cfg.WebSearchExtensions(); len(out) != 0 {
		t.Fatalf("mixed instance emitted extensions: %+v", out)
	}
}

func TestWebSearchExtensionsIgnoresNonGenerateModels(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("openai", "inst-aaa", "responses", true),
	}}
	cfg.Instances[0].Models = append(cfg.Instances[0].Models, Model{
		Name: "text-embedding-3-small",
		Kind: "embed",
	})
	out := cfg.WebSearchExtensions()
	if len(out) != 1 || out[0].Provider != "openai-inst-aaa" {
		t.Fatalf("extensions = %+v, want openai-inst-aaa only", out)
	}
}

func TestWebSearchExtensionsBytedanceEmptyKnobs(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("bytedance", "inst-aaa", "", true, true),
	}}
	out := cfg.WebSearchExtensions()
	if len(out) != 1 {
		t.Fatalf("extensions = %+v, want one", out)
	}
	ws, ok := out[0].Fields["web_search"].(map[string]any)
	if !ok {
		t.Fatalf("fields = %+v, want web_search knob", out[0].Fields)
	}
	if _, hasToolChoice := ws["tool_choice"]; hasToolChoice {
		t.Fatalf("bytedance web_search must not carry tool_choice: %+v", ws)
	}
}

func TestWebSearchExtensionsSkipsDisabledAndUnsupported(t *testing.T) {
	disabled := webSearchInstance("deepseek", "inst-aaa", "responses", true)
	disabled.Enabled = false
	anthropic := webSearchInstance("anthropic", "inst-aaa", "", true)
	cfg := InferenceConfig{Instances: []Instance{disabled, anthropic}}
	if out := cfg.WebSearchExtensions(); len(out) != 0 {
		t.Fatalf("extensions = %+v, want none", out)
	}
}

func TestWebSearchExtensionsAzure(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("azure", "inst-aaa", "", true),
	}}
	out := cfg.WebSearchExtensions()
	if len(out) != 1 || out[0].Provider != "azure-inst-aaa" {
		t.Fatalf("extensions = %+v, want azure-inst-aaa", out)
	}
}

func TestWebSearchExtensionsMultipleInstancesSameType(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("deepseek", "inst-a", "responses", true),
		webSearchInstance("deepseek", "inst-b", "responses", true),
	}}
	out := cfg.WebSearchExtensions()
	if len(out) != 2 {
		t.Fatalf("extensions = %+v, want two", out)
	}
	if out[0].Provider != "deepseek-inst-a" ||
		out[1].Provider != "deepseek-inst-b" {
		t.Fatalf("extensions = %+v, want distinct deployment ids", out)
	}
}

func TestWebSearchExtensionsDefaultsToResponsesWhenApiEmpty(t *testing.T) {
	// The catalog default for OpenAI/DeepSeek is responses, and the
	// writer always emits it, so an empty API field on an in-memory
	// instance must not suppress the extension.
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("deepseek", "inst-aaa", "", true),
	}}
	if out := cfg.WebSearchExtensions(); len(out) != 1 {
		t.Fatalf("extensions = %+v, want one", out)
	}
}

func TestWebSearchExtensionsLegacyDeploymentID(t *testing.T) {
	cfg := InferenceConfig{Instances: []Instance{
		webSearchInstance("deepseek", "", "responses", true),
	}}
	out := cfg.WebSearchExtensions()
	if len(out) != 1 || out[0].Provider != "deepseek-1" {
		t.Fatalf("extensions = %+v, want deepseek-1", out)
	}
}

func TestWebSearchExtensionsSkipsLegacyIdWhenDisabledPresent(t *testing.T) {
	// A disabled instance reorders LoadInference output, so a
	// position-derived deployment id cannot be trusted; emitting it
	// would reference a provider the runtime never registered.
	legacy := webSearchInstance("deepseek", "", "responses", true)
	disabled := webSearchInstance("openai", "", "responses", true)
	disabled.Enabled = false
	cfg := InferenceConfig{Instances: []Instance{legacy, disabled}}
	if out := cfg.WebSearchExtensions(); len(out) != 0 {
		t.Fatalf("extensions = %+v, want none for ambiguous legacy id", out)
	}
}

// TestAssistantGraphWebSearchIsBoardDriven guards the default graph
// against drifting back to static driver-type extension entries, which
// flowcraft never matches against suffixed deployment ids.
func TestAssistantGraphWebSearchIsBoardDriven(t *testing.T) {
	data, err := FS().ReadFile("assets/graphs/assistant.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, "extensions: ${board:llm_extensions:[]}") {
		t.Fatalf("assistant.yaml must consume board:llm_extensions:\n%s", doc)
	}
	for _, stale := range []string{
		"- provider: deepseek",
		"- provider: openai",
		"- provider: bytedance",
	} {
		if strings.Contains(doc, stale) {
			t.Fatalf("assistant.yaml still addresses static %q:\n%s", stale, doc)
		}
	}
}
