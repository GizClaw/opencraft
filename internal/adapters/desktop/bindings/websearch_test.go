package bindings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func webSearchBinding(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	return NewConfig(core.NewCore(dir, dir, dir))
}

func TestWebSearchConfigDefaults(t *testing.T) {
	b := webSearchBinding(t)
	state, err := b.WebSearchConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.Provider != config.WebSearchProviderAuto {
		t.Fatalf("state = %+v", state)
	}
	if state.MaxResults != config.WebSearchDefaultMaxResults {
		t.Fatalf("max_results = %d", state.MaxResults)
	}
	if len(state.Providers) != 4 {
		t.Fatalf("providers = %+v", state.Providers)
	}
	keyless := map[string]bool{}
	for _, p := range state.Providers {
		keyless[p.ID] = p.Keyless
		if p.Endpoint == "" {
			t.Fatalf("provider %s has no effective endpoint", p.ID)
		}
		if p.KeySet {
			t.Fatalf("provider %s reports a key before any save", p.ID)
		}
	}
	if !keyless[config.WebSearchProviderParallel] ||
		!keyless[config.WebSearchProviderExa] ||
		keyless[config.WebSearchProviderTavily] ||
		keyless[config.WebSearchProviderBrave] {
		t.Fatalf("keyless flags = %+v", keyless)
	}
}

func TestSaveWebSearchStoresAndClearsKey(t *testing.T) {
	b := webSearchBinding(t)
	err := b.SaveWebSearch(WebSearchRequest{
		Enabled:    true,
		Provider:   config.WebSearchProviderBrave,
		MaxResults: 5,
		Timeout:    "20s",
		Keys:       map[string]string{config.WebSearchProviderBrave: "bv-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := b.WebSearchConfig()
	if err != nil {
		t.Fatal(err)
	}
	if state.Provider != config.WebSearchProviderBrave ||
		state.MaxResults != 5 || state.Timeout != "20s" {
		t.Fatalf("state = %+v", state)
	}
	var braveSet bool
	for _, p := range state.Providers {
		if p.ID == config.WebSearchProviderBrave {
			braveSet = p.KeySet
		}
	}
	if !braveSet {
		t.Fatal("brave key must report as set after save")
	}
	stored, found, err := b.core.Plugin.Secrets.Get(context.Background(),
		config.WebSearchSecretAccount(config.WebSearchProviderBrave))
	if err != nil || !found || stored != "bv-123" {
		t.Fatalf("stored key = (%q, %v, %v)", stored, found, err)
	}

	if err := b.SaveWebSearch(WebSearchRequest{
		Enabled:   true,
		Provider:  config.WebSearchProviderAuto,
		Timeout:   "15s",
		ClearKeys: []string{config.WebSearchProviderBrave},
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := b.core.Plugin.Secrets.Get(context.Background(),
		config.WebSearchSecretAccount(config.WebSearchProviderBrave)); err != nil ||
		found {
		t.Fatalf("cleared key still present (found=%v err=%v)", found, err)
	}
	settings, err := config.LoadWebSearch(b.core.UserDir)
	if err != nil {
		t.Fatal(err)
	}
	if settings.APIKeys.Brave != "" {
		t.Fatalf("settings kept a stale ref: %+v", settings.APIKeys)
	}
}

func TestSaveWebSearchRejectsKeyWithoutStore(t *testing.T) {
	b := webSearchBinding(t)
	b.core.Plugin.Secrets = nil
	if err := b.SaveWebSearch(WebSearchRequest{
		Enabled:  true,
		Provider: config.WebSearchProviderTavily,
		Timeout:  "15s",
		Keys:     map[string]string{config.WebSearchProviderTavily: "tv"},
	}); err == nil {
		t.Fatal("saving a key without a credential store must fail")
	}
}

func TestTestWebSearchRunsOneProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w,
				`event: message
data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":`+
					`"Title: Go\nURL: https://go.dev/\nPublished: N/A\nAuthor: N/A\nHighlights:\nGo site"}]}}

`)
		}))
	defer srv.Close()
	b := webSearchBinding(t)
	out, err := b.TestWebSearch(WebSearchTestRequest{
		Provider: config.WebSearchProviderExa,
		Query:    "go",
		Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != config.WebSearchProviderExa ||
		len(out.Results) != 1 ||
		out.Results[0].URL != "https://go.dev/" {
		t.Fatalf("out = %+v", out)
	}
}
