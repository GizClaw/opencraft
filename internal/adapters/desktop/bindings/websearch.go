package bindings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/opencraft/internal/capabilities/tools/websearch"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Web search settings as the tools tab consumes them: foundation/config
// owns the shape, this layer maps it into DTOs and performs the
// credential-store writes the page asks for.

// WebSearchEndpointsDTO carries the optional per-provider endpoint
// overrides.
type WebSearchEndpointsDTO struct {
	Exa      string `json:"exa,omitempty"`
	Parallel string `json:"parallel,omitempty"`
	Tavily   string `json:"tavily,omitempty"`
	Brave    string `json:"brave,omitempty"`
}

// WebSearchProviderView is one provider row of the card.
type WebSearchProviderView struct {
	ID       string `json:"id"`
	Keyless  bool   `json:"keyless"`
	KeySet   bool   `json:"key_set"`
	Endpoint string `json:"endpoint"`
	// Override is the user-typed endpoint, empty when the vendor
	// default is in effect.
	Override string `json:"override,omitempty"`
}

// WebSearchState is the whole state of the web search card.
type WebSearchState struct {
	Enabled    bool                    `json:"enabled"`
	Provider   string                  `json:"provider"`
	MaxResults int                     `json:"max_results"`
	Timeout    string                  `json:"timeout"`
	Endpoints  WebSearchEndpointsDTO   `json:"endpoints"`
	Providers  []WebSearchProviderView `json:"providers"`
	// SecretsAvailable reports whether the credential store can accept
	// new keys; the card disables the key input when false.
	SecretsAvailable bool `json:"secrets_available"`
}

// WebSearchRequest is the save payload. Keys carries newly typed
// credentials (empty values are ignored), ClearKeys names providers
// whose stored credential must be removed.
type WebSearchRequest struct {
	Enabled    bool                  `json:"enabled"`
	Provider   string                `json:"provider"`
	MaxResults int                   `json:"max_results"`
	Timeout    string                `json:"timeout"`
	Endpoints  WebSearchEndpointsDTO `json:"endpoints"`
	Keys       map[string]string     `json:"keys,omitempty"`
	ClearKeys  []string              `json:"clearKeys,omitempty"`
}

// WebSearchTestRequest is one "test query" invocation from the card.
type WebSearchTestRequest struct {
	Provider   string `json:"provider"`
	Query      string `json:"query"`
	APIKey     string `json:"apiKey,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

// WebSearchTestHit is one result row of the test run.
type WebSearchTestHit struct {
	Title     string `json:"title,omitempty"`
	URL       string `json:"url"`
	Snippet   string `json:"snippet,omitempty"`
	Published string `json:"published,omitempty"`
}

// WebSearchTestResult reports one real provider call.
type WebSearchTestResult struct {
	Provider string             `json:"provider"`
	Results  []WebSearchTestHit `json:"results"`
	Context  string             `json:"context,omitempty"`
}

// keylessProviderSet marks the providers that work without a key.
func keylessProviderSet() map[string]bool {
	out := map[string]bool{}
	for _, id := range config.WebSearchKeylessProviders() {
		out[id] = true
	}
	return out
}

// WebSearchConfig returns the persisted settings plus, per provider,
// whether a credential is present and which endpoint is in effect.
func (b *Config) WebSearchConfig() (WebSearchState, error) {
	ctx := b.core.Shell.Context()
	loaded, err := config.LoadWebSearch(b.core.UserDir)
	if err != nil {
		return WebSearchState{}, err
	}
	effective, err := loaded.Resolve()
	if err != nil {
		return WebSearchState{}, err
	}
	keyless := keylessProviderSet()
	out := WebSearchState{
		Enabled:    effective.Enabled,
		Provider:   effective.Provider,
		MaxResults: effective.MaxResults,
		Timeout:    effective.Timeout.String(),
		Endpoints: WebSearchEndpointsDTO{
			Exa:      loaded.Endpoints.Exa,
			Parallel: loaded.Endpoints.Parallel,
			Tavily:   loaded.Endpoints.Tavily,
			Brave:    loaded.Endpoints.Brave,
		},
		SecretsAvailable: b.secretsAvailable(),
	}
	for _, id := range config.WebSearchProviders() {
		if id == config.WebSearchProviderAuto {
			continue
		}
		out.Providers = append(out.Providers, WebSearchProviderView{
			ID:       id,
			Keyless:  keyless[id],
			KeySet:   b.webSearchKeySet(ctx, loaded, id),
			Endpoint: effective.Endpoints[id],
			Override: loaded.Endpoints.Get(id),
		})
	}
	return out, nil
}

// SaveWebSearch persists the settings, storing newly typed keys in the
// credential store and rewriting the settings reference to point at
// it. The document reload makes the tool pick the change up without an
// app restart.
func (b *Config) SaveWebSearch(req WebSearchRequest) error {
	ctx := b.core.Shell.Context()
	next, err := config.LoadWebSearch(b.core.UserDir)
	if err != nil {
		return err
	}
	enabled := req.Enabled
	next.Enabled = &enabled
	next.Provider = req.Provider
	next.MaxResults = req.MaxResults
	next.Timeout = req.Timeout
	next.Endpoints = config.WebSearchEndpoints{
		Exa:      strings.TrimSpace(req.Endpoints.Exa),
		Parallel: strings.TrimSpace(req.Endpoints.Parallel),
		Tavily:   strings.TrimSpace(req.Endpoints.Tavily),
		Brave:    strings.TrimSpace(req.Endpoints.Brave),
	}
	for provider, key := range req.Keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !config.ValidWebSearchProvider(provider) ||
			provider == config.WebSearchProviderAuto {
			return fmt.Errorf(
				"web search: unknown provider %q in keys", provider)
		}
		if !b.secretsAvailable() {
			return fmt.Errorf(
				"web search: credential store is unavailable")
		}
		if err := b.core.Plugin.Secrets.Set(
			ctx, config.WebSearchSecretAccount(provider), key); err != nil {
			return err
		}
		next.APIKeys.Set(provider, config.WebSearchSecretRef(provider))
	}
	for _, provider := range req.ClearKeys {
		if !config.ValidWebSearchProvider(provider) ||
			provider == config.WebSearchProviderAuto {
			return fmt.Errorf(
				"web search: unknown provider %q in clearKeys", provider)
		}
		if b.secretsAvailable() {
			if err := b.core.Plugin.Secrets.Delete(
				ctx, config.WebSearchSecretAccount(provider)); err != nil {
				return err
			}
		}
		next.APIKeys.Set(provider, "")
	}
	if err := config.SaveWebSearch(b.core.UserDir, next); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(ctx)
}

// TestWebSearch runs one real query through the selected provider and
// returns the first hits, so the card can prove the configuration
// before saving.
func (b *Config) TestWebSearch(
	req WebSearchTestRequest,
) (WebSearchTestResult, error) {
	ctx := b.core.Shell.Context()
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if !config.ValidWebSearchProvider(provider) ||
		provider == config.WebSearchProviderAuto {
		return WebSearchTestResult{}, fmt.Errorf(
			"web search: pick a provider before testing")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return WebSearchTestResult{}, fmt.Errorf(
			"web search: enter a test query")
	}
	key := strings.TrimSpace(req.APIKey)
	if key == "" && b.secretsAvailable() {
		if stored, found, err := b.core.Plugin.Secrets.Get(
			ctx, config.WebSearchSecretAccount(provider)); err == nil && found {
			key = stored
		}
	}
	settings := config.WebSearchSettings{
		Provider:   provider,
		MaxResults: 3,
		Timeout:    "20s",
	}
	settings.APIKeys.Set(provider, key)
	if endpoint := strings.TrimSpace(req.Endpoint); endpoint != "" {
		settings.Endpoints.Set(provider, endpoint)
	}
	tool, err := websearch.New(settings)
	if err != nil {
		return WebSearchTestResult{}, err
	}
	args, err := json.Marshal(map[string]any{"query": query, "count": 3})
	if err != nil {
		return WebSearchTestResult{}, err
	}
	content, err := tool.Execute(ctx, string(args))
	if err != nil {
		return WebSearchTestResult{}, err
	}
	var envelope struct {
		Provider string `json:"provider"`
		Results  []struct {
			Title     string `json:"title"`
			URL       string `json:"url"`
			Snippet   string `json:"snippet"`
			Published string `json:"published"`
		} `json:"results"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(content.Text()), &envelope); err != nil {
		return WebSearchTestResult{}, fmt.Errorf(
			"web search: decode test result: %w", err)
	}
	out := WebSearchTestResult{
		Provider: envelope.Provider,
		Context:  envelope.Context,
		Results:  make([]WebSearchTestHit, 0, len(envelope.Results)),
	}
	for _, hit := range envelope.Results {
		out.Results = append(out.Results, WebSearchTestHit{
			Title:     hit.Title,
			URL:       hit.URL,
			Snippet:   hit.Snippet,
			Published: hit.Published,
		})
	}
	return out, nil
}

// secretsAvailable reports whether the credential store is usable.
func (b *Config) secretsAvailable() bool {
	return b.core.Plugin != nil &&
		b.core.Plugin.Secrets != nil &&
		b.core.Plugin.Secrets.Available()
}

// webSearchKeySet reports whether a credential is configured for a
// provider, either as a settings value/reference or in the store.
func (b *Config) webSearchKeySet(
	ctx context.Context,
	settings config.WebSearchSettings,
	provider string,
) bool {
	if settings.APIKeys.Get(provider) != "" {
		return true
	}
	if !b.secretsAvailable() {
		return false
	}
	_, found, err := b.core.Plugin.Secrets.Get(
		ctx, config.WebSearchSecretAccount(provider))
	return err == nil && found
}
