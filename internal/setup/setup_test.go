package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/config"
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

func envKeyed(t *testing.T, ids ...string) Config {
	t.Helper()
	var cfg Config
	for _, id := range ids {
		cfg.Providers = append(cfg.Providers, KeyedProvider{
			Provider:  mustProvider(t, id),
			KeySource: KeyEnv,
		})
	}
	return cfg
}

func load(t *testing.T, workDir, userDir string) *config.View {
	t.Helper()
	mgr, err := config.Open(config.Options{WorkDir: workDir, UserDir: userDir})
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

	needed, err := Needed(dir)
	if err != nil || !needed {
		t.Fatalf("empty dir: needed=%v err=%v, want true", needed, err)
	}

	// A user layer without router targets is unconfigured (the embedded
	// inference layer provides providers/infer/router shell).
	writeFile(t, dir, "opencraft.yaml", "resources:\n  box:\n    impl: local\n")
	needed, err = Needed(dir)
	if err != nil || !needed {
		t.Fatalf("no router: needed=%v err=%v, want true", needed, err)
	}

	// A router declaration (setup output) counts as configured.
	writeFile(t, dir, "opencraft.yaml",
		"resources:\n  router:\n    settings:\n      generate:\n")
	needed, err = Needed(dir)
	if err != nil || needed {
		t.Fatalf("with router: needed=%v err=%v, want false", needed, err)
	}

	// Unparseable user layer is treated as unconfigured.
	writeFile(t, dir, "opencraft.yaml", ":: not yaml [")
	needed, err = Needed(dir)
	if err != nil || !needed {
		t.Fatalf("broken yaml: needed=%v err=%v, want true", needed, err)
	}
}

func TestUserConfigYAMLVariableParts(t *testing.T) {
	cfg := envKeyed(t, "deepseek", "openai")
	data, err := cfg.UserConfigYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	// Only variable parts: key profiles + router generate targets.
	if !strings.Contains(doc, "api_key: ${env:DEEPSEEK_API_KEY}") {
		t.Fatalf("deepseek profile missing:\n%s", doc)
	}
	if !strings.Contains(doc, "api_key: ${env:OPENAI_API_KEY}") {
		t.Fatalf("openai profile missing:\n%s", doc)
	}
	if strings.Contains(doc, "api_key: ${env:ANTHROPIC_API_KEY}") {
		t.Fatalf("unkeyed anthropic must not carry a profile:\n%s", doc)
	}
	// The fixed wiring is NOT duplicated in the user layer.
	for _, name := range []string{"kind: inference.Provider", "kind: inference.Assembly",
		"kind: inference.Router", "retry:", "fallback_on_retry_exhausted"} {
		if strings.Contains(doc, name) {
			t.Fatalf("user layer must not duplicate fixed wiring %q:\n%s", name, doc)
		}
	}
	// Router targets = keyed providers in priority order.
	idx := strings.Index(doc, "provider: deepseek")
	idx2 := strings.Index(doc, "provider: openai")
	if idx < 0 || idx2 < 0 || idx > idx2 {
		t.Fatalf("router priority order wrong:\n%s", doc)
	}
	if strings.Contains(doc, "provider: anthropic") {
		t.Fatalf("unkeyed provider must not be a router target:\n%s", doc)
	}
}

func TestUserConfigYAMLAzure(t *testing.T) {
	cfg := envKeyed(t, "deepseek")
	cfg.Providers = append(cfg.Providers, KeyedProvider{
		Provider:  mustProvider(t, "azure"),
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Model:     "gpt-5.6-sol-deploy",
	})
	data, err := cfg.UserConfigYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"provider.azure:",
		"endpoint: 'https://res.openai.azure.com'",
		"name: 'gpt-5.6-sol-deploy'",
		"kind: generate",
		"capabilities:",
		"outputs: [text]",
		"provider.azure: provider.azure", // infer dep merge
		"provider: azure",                // router target
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("azure doc missing %q:\n%s", want, doc)
		}
	}

	// Missing endpoint/model must fail generation.
	bad := envKeyed(t, "deepseek")
	bad.Providers = append(bad.Providers, KeyedProvider{Provider: mustProvider(t, "azure")})
	if _, err := bad.UserConfigYAML(); err == nil {
		t.Fatal("azure without endpoint must fail")
	}
}

func TestUserConfigYAMLAzureCapabilities(t *testing.T) {
	cfg := envKeyed(t, "deepseek")
	cfg.Providers = append(cfg.Providers, KeyedProvider{
		Provider:  mustProvider(t, "azure"),
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Model:     "gpt-5.6-sol-deploy",
		Vision:    true,
		Reasoning: "toggle",
		WebSearch: true,
	})
	data, err := cfg.UserConfigYAML()
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
	off.Providers = append(off.Providers, KeyedProvider{
		Provider:  mustProvider(t, "azure"),
		KeySource: KeyEnv,
		Endpoint:  "https://res.openai.azure.com",
		Model:     "gpt-5.6-sol-deploy",
	})
	data, err = off.UserConfigYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "reasoning:") {
		t.Fatalf("default azure must not declare reasoning:\n%s", data)
	}
}

func TestLiteralKeyQuoted(t *testing.T) {
	cfg := Config{Providers: []KeyedProvider{{
		Provider:  mustProvider(t, "deepseek"),
		KeySource: KeyLiteral,
		KeyValue:  "sk-it's-secret",
	}}}
	data, err := cfg.UserConfigYAML()
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
	if err := cfg.Write(dir); err != nil {
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

	needed, err := Needed(dir)
	if err != nil || needed {
		t.Fatalf("after write: needed=%v err=%v", needed, err)
	}
	if got := config.DefaultModel(dir); got != "deepseek/deepseek-v4-flash" {
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
}
