package websearch

import (
	"strings"
	"testing"
	"time"

	occonfig "github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestSettingsResolveDefaults(t *testing.T) {
	cfg, err := (Settings{}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("enabled must default to true")
	}
	if cfg.Provider != ProviderAuto {
		t.Fatalf("provider = %q, want auto", cfg.Provider)
	}
	if cfg.MaxResults != DefaultMaxResults {
		t.Fatalf("max_results = %d, want %d", cfg.MaxResults, DefaultMaxResults)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("timeout = %s, want %s", cfg.Timeout, DefaultTimeout)
	}
	if cfg.Endpoints[ProviderExa] != occonfig.WebSearchDefaultEndpoint(ProviderExa) ||
		cfg.Endpoints[ProviderParallel] != occonfig.WebSearchDefaultEndpoint(ProviderParallel) ||
		cfg.Endpoints[ProviderTavily] != occonfig.WebSearchDefaultEndpoint(ProviderTavily) ||
		cfg.Endpoints[ProviderBrave] != occonfig.WebSearchDefaultEndpoint(ProviderBrave) {
		t.Fatalf("endpoints = %+v", cfg.Endpoints)
	}
}

func TestSettingsResolveRejectsBadValues(t *testing.T) {
	for name, settings := range map[string]Settings{
		"unknown provider": {Provider: "ddg"},
		"max_results high": {MaxResults: MaxMaxResults + 1},
		"max_results low":  {MaxResults: -1},
		"timeout parse":    {Timeout: "soon"},
		"timeout low":      {Timeout: "500ms"},
		"timeout high":     {Timeout: "120s"},
		"tavily no key":    {Provider: ProviderTavily},
		"brave no key":     {Provider: ProviderBrave},
		"endpoint public http": {
			Endpoints: Endpoints{Exa: "http://mcp.example.com/mcp"},
		},
		"endpoint query": {
			Endpoints: Endpoints{Exa: "https://mcp.example.com/mcp?token=x"},
		},
		"endpoint userinfo": {
			Endpoints: Endpoints{Exa: "https://user:pass@mcp.example.com/mcp"},
		},
		"endpoint scheme": {
			Endpoints: Endpoints{Exa: "ftp://mcp.example.com/mcp"},
		},
	} {
		if _, err := settings.Resolve(); err == nil {
			t.Errorf("%s: Resolve accepted bad settings", name)
		}
	}
}

func TestSettingsResolveAcceptsLoopbackHTTPAndKeys(t *testing.T) {
	cfg, err := (Settings{
		Provider:   ProviderTavily,
		MaxResults: 3,
		Timeout:    "20s",
		APIKeys:    APIKeys{Tavily: "tv-key"},
		Endpoints:  Endpoints{Tavily: "http://127.0.0.1:8080/search"},
	}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderTavily || cfg.MaxResults != 3 ||
		cfg.Timeout != 20*time.Second {
		t.Fatalf("resolved = %+v", cfg)
	}
	if got := cfg.APIKeys.Get(ProviderTavily); got != "tv-key" {
		t.Fatalf("tavily key = %q", got)
	}
}

func TestTruncateRunesKeepsBoundaries(t *testing.T) {
	got := truncateRunes("héllo wörld", 5)
	if !strings.HasPrefix(got, "héllo") || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateRunes = %q", got)
	}
	if cleanSpace("a \n b\t\tc ") != "a b c" {
		t.Fatalf("cleanSpace = %q", cleanSpace("a \n b\t\tc "))
	}
}
