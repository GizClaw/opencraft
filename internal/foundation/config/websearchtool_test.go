package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebSearchLoadDefaultsWhenUnset(t *testing.T) {
	dir := t.TempDir()
	settings, err := LoadWebSearch(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Provider != WebSearchProviderAuto ||
		cfg.MaxResults != WebSearchDefaultMaxResults {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestWebSearchSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencraft.yaml")
	// Simulate an existing hand-written resource that must survive.
	if err := os.WriteFile(path, []byte(
		"version: v1\nresources:\n  tool.mcp:\n    kind: tool.Source\n"+
			"    impl: mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := WebSearchSettings{
		Provider:   WebSearchProviderBrave,
		MaxResults: 4,
		Timeout:    "20s",
		APIKeys: WebSearchAPIKeys{
			Brave: WebSearchSecretRef(WebSearchProviderBrave),
		},
		Endpoints: WebSearchEndpoints{
			Brave: "http://127.0.0.1:8080/search",
		},
	}
	if err := SaveWebSearch(dir, saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWebSearch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != WebSearchProviderBrave ||
		loaded.MaxResults != 4 || loaded.Timeout != "20s" ||
		loaded.APIKeys.Brave != WebSearchSecretRef(WebSearchProviderBrave) ||
		loaded.Endpoints.Brave != "http://127.0.0.1:8080/search" {
		t.Fatalf("loaded = %+v", loaded)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "tool.mcp") {
		t.Fatalf("unrelated resource was dropped:\n%s", raw)
	}
}

func TestWebSearchSaveReplacesClearedKeys(t *testing.T) {
	dir := t.TempDir()
	if err := SaveWebSearch(dir, WebSearchSettings{
		Provider: WebSearchProviderTavily,
		APIKeys: WebSearchAPIKeys{
			Tavily: WebSearchSecretRef(WebSearchProviderTavily),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SaveWebSearch(dir, WebSearchSettings{
		Provider: WebSearchProviderAuto,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWebSearch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKeys.Tavily != "" {
		t.Fatalf("cleared key survived: %+v", loaded)
	}
}

func TestWebSearchSaveValidates(t *testing.T) {
	dir := t.TempDir()
	if err := SaveWebSearch(dir, WebSearchSettings{
		MaxResults: WebSearchMaxMaxResults + 1,
	}); err == nil {
		t.Fatal("SaveWebSearch accepted an out-of-range knob")
	}
	if _, err := os.Stat(filepath.Join(dir, "opencraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("invalid settings must not write the user layer: %v", err)
	}
}

func TestWebSearchSecretRef(t *testing.T) {
	if got := WebSearchSecretRef(WebSearchProviderBrave); got !=
		"${secret:keychain.websearch/brave}" {
		t.Fatalf("ref = %q", got)
	}
}
