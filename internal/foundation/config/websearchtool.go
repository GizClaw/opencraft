// Web search tool settings.
//
// This file owns the tool.websearch settings shape, its defaults and
// validation, and the user-layer persistence the settings page uses.
// The web_search tool consumes the same shape through a type alias, so
// the page and the runtime cannot drift.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Provider ids accepted by the provider setting.
const (
	WebSearchProviderAuto     = "auto"
	WebSearchProviderExa      = "exa"
	WebSearchProviderParallel = "parallel"
	WebSearchProviderTavily   = "tavily"
	WebSearchProviderBrave    = "brave"
)

// Bounds and defaults for the tool settings.
const (
	WebSearchDefaultMaxResults = 8
	WebSearchMinMaxResults     = 1
	WebSearchMaxMaxResults     = 10

	WebSearchDefaultTimeout = 15 * time.Second
	WebSearchMinTimeout     = time.Second
	WebSearchMaxTimeout     = 60 * time.Second
)

// webSearchDefaultEndpoints maps each provider to the endpoint used
// when the settings carry no override. The Parallel and Exa entries
// are the vendors' hosted MCP servers (keyless, documented free tier);
// Tavily and Brave are their REST APIs.
var webSearchDefaultEndpoints = map[string]string{
	WebSearchProviderExa:      "https://mcp.exa.ai/mcp",
	WebSearchProviderParallel: "https://search.parallel.ai/mcp",
	WebSearchProviderTavily:   "https://api.tavily.com/search",
	WebSearchProviderBrave:    "https://api.search.brave.com/res/v1/web/search",
}

// WebSearchKeylessProviders lists the providers that need no
// credential, best first: the tool's auto mode picks among them.
func WebSearchKeylessProviders() []string {
	return []string{WebSearchProviderParallel, WebSearchProviderExa}
}

// WebSearchDefaultEndpoint returns the vendor endpoint for a provider
// (empty for an unknown id).
func WebSearchDefaultEndpoint(provider string) string {
	return webSearchDefaultEndpoints[provider]
}

// WebSearchProviders lists every provider id the settings accept.
func WebSearchProviders() []string {
	return []string{
		WebSearchProviderAuto,
		WebSearchProviderExa,
		WebSearchProviderParallel,
		WebSearchProviderTavily,
		WebSearchProviderBrave,
	}
}

// ValidWebSearchProvider reports whether id is one of the accepted
// provider ids.
func ValidWebSearchProvider(id string) bool {
	_, ok := webSearchDefaultEndpoints[id]
	if ok {
		return true
	}
	return id == WebSearchProviderAuto
}

// WebSearchSecretAccount returns the keyring account name for one
// provider key.
func WebSearchSecretAccount(provider string) string {
	return "websearch/" + provider
}

// WebSearchSecretRef renders the deployment settings reference for one
// provider key; the runtime builder expands it before the tool factory
// runs, so the plaintext never lands in the config file.
func WebSearchSecretRef(provider string) string {
	return "${secret:keychain." + WebSearchSecretAccount(provider) + "}"
}

// WebSearchAPIKeys carries one optional credential per provider.
// Values are literals or deployment references (${env:...},
// ${secret:...}) expanded by the runtime builder.
type WebSearchAPIKeys struct {
	Exa      string `json:"exa,omitempty"`
	Parallel string `json:"parallel,omitempty"`
	Tavily   string `json:"tavily,omitempty"`
	Brave    string `json:"brave,omitempty"`
}

// Get returns the credential configured for one provider.
func (k WebSearchAPIKeys) Get(provider string) string {
	switch provider {
	case WebSearchProviderExa:
		return strings.TrimSpace(k.Exa)
	case WebSearchProviderParallel:
		return strings.TrimSpace(k.Parallel)
	case WebSearchProviderTavily:
		return strings.TrimSpace(k.Tavily)
	case WebSearchProviderBrave:
		return strings.TrimSpace(k.Brave)
	default:
		return ""
	}
}

// Set stores the credential for one provider.
func (k *WebSearchAPIKeys) Set(provider, value string) {
	switch provider {
	case WebSearchProviderExa:
		k.Exa = value
	case WebSearchProviderParallel:
		k.Parallel = value
	case WebSearchProviderTavily:
		k.Tavily = value
	case WebSearchProviderBrave:
		k.Brave = value
	}
}

// WebSearchEndpoints overrides provider base URLs (gateways,
// self-hosted proxies). Empty keeps the vendor default.
type WebSearchEndpoints struct {
	Exa      string `json:"exa,omitempty"`
	Parallel string `json:"parallel,omitempty"`
	Tavily   string `json:"tavily,omitempty"`
	Brave    string `json:"brave,omitempty"`
}

// Get returns the endpoint override for one provider.
func (e WebSearchEndpoints) Get(provider string) string {
	switch provider {
	case WebSearchProviderExa:
		return strings.TrimSpace(e.Exa)
	case WebSearchProviderParallel:
		return strings.TrimSpace(e.Parallel)
	case WebSearchProviderTavily:
		return strings.TrimSpace(e.Tavily)
	case WebSearchProviderBrave:
		return strings.TrimSpace(e.Brave)
	default:
		return ""
	}
}

// Set stores the endpoint override for one provider.
func (e *WebSearchEndpoints) Set(provider, value string) {
	switch provider {
	case WebSearchProviderExa:
		e.Exa = value
	case WebSearchProviderParallel:
		e.Parallel = value
	case WebSearchProviderTavily:
		e.Tavily = value
	case WebSearchProviderBrave:
		e.Brave = value
	}
}

// WebSearchSettings is the tool.websearch settings subtree.
type WebSearchSettings struct {
	Enabled    *bool              `json:"enabled,omitempty"`
	Provider   string             `json:"provider,omitempty"`
	MaxResults int                `json:"max_results,omitempty"`
	Timeout    string             `json:"timeout,omitempty"`
	APIKeys    WebSearchAPIKeys   `json:"api_keys,omitempty"`
	Endpoints  WebSearchEndpoints `json:"endpoints,omitempty"`
}

// WebSearchConfig is the validated, defaulted view the tool uses.
type WebSearchConfig struct {
	Enabled    bool
	Provider   string
	MaxResults int
	Timeout    time.Duration
	APIKeys    WebSearchAPIKeys
	Endpoints  map[string]string
}

// Resolve validates the settings and applies defaults. It rejects
// unknown providers, out-of-range knobs, keyed providers without a
// key, and endpoints that would leak credentials or send a key in
// cleartext: https everywhere, http only for loopback endpoints.
func (s WebSearchSettings) Resolve() (WebSearchConfig, error) {
	out := WebSearchConfig{
		Enabled:    s.Enabled == nil || *s.Enabled,
		Provider:   strings.ToLower(strings.TrimSpace(s.Provider)),
		MaxResults: s.MaxResults,
		APIKeys:    s.APIKeys,
		Endpoints:  map[string]string{},
	}
	if out.Provider == "" {
		out.Provider = WebSearchProviderAuto
	}
	switch out.Provider {
	case WebSearchProviderAuto, WebSearchProviderExa,
		WebSearchProviderParallel, WebSearchProviderTavily,
		WebSearchProviderBrave:
	default:
		return WebSearchConfig{}, fmt.Errorf(
			"web_search: unknown provider %q "+
				"(want auto, exa, parallel, tavily or brave)",
			out.Provider)
	}
	if out.MaxResults == 0 {
		out.MaxResults = WebSearchDefaultMaxResults
	}
	if out.MaxResults < WebSearchMinMaxResults ||
		out.MaxResults > WebSearchMaxMaxResults {
		return WebSearchConfig{}, fmt.Errorf(
			"web_search: max_results %d out of range (%d-%d)",
			out.MaxResults,
			WebSearchMinMaxResults, WebSearchMaxMaxResults)
	}
	raw := strings.TrimSpace(s.Timeout)
	if raw == "" {
		out.Timeout = WebSearchDefaultTimeout
	} else {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return WebSearchConfig{}, fmt.Errorf(
				"web_search: parse timeout %q: %w", raw, err)
		}
		if d < WebSearchMinTimeout || d > WebSearchMaxTimeout {
			return WebSearchConfig{}, fmt.Errorf(
				"web_search: timeout %s out of range (%s-%s)",
				d, WebSearchMinTimeout, WebSearchMaxTimeout)
		}
		out.Timeout = d
	}
	if out.Provider == WebSearchProviderTavily &&
		out.APIKeys.Get(WebSearchProviderTavily) == "" {
		return WebSearchConfig{}, fmt.Errorf(
			"web_search: provider tavily requires api_keys.tavily")
	}
	if out.Provider == WebSearchProviderBrave &&
		out.APIKeys.Get(WebSearchProviderBrave) == "" {
		return WebSearchConfig{}, fmt.Errorf(
			"web_search: provider brave requires api_keys.brave")
	}
	for _, provider := range []string{
		WebSearchProviderExa, WebSearchProviderParallel,
		WebSearchProviderTavily, WebSearchProviderBrave,
	} {
		ep := s.Endpoints.Get(provider)
		if ep == "" {
			out.Endpoints[provider] = webSearchDefaultEndpoints[provider]
			continue
		}
		if err := validateWebSearchEndpoint(provider, ep); err != nil {
			return WebSearchConfig{}, err
		}
		out.Endpoints[provider] = ep
	}
	return out, nil
}

// validateWebSearchEndpoint checks one endpoint override. Query
// strings and fragments are rejected because credentials belong in
// headers, and plain http is limited to loopback so a key cannot
// cross the network in cleartext.
func validateWebSearchEndpoint(provider, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf(
			"web_search: endpoints.%s: parse %q: %w", provider, raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf(
			"web_search: endpoints.%s: unsupported scheme %q",
			provider, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf(
			"web_search: endpoints.%s: missing host", provider)
	}
	if u.User != nil {
		return fmt.Errorf(
			"web_search: endpoints.%s: userinfo is not allowed", provider)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf(
			"web_search: endpoints.%s: query and fragment are not allowed",
			provider)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return fmt.Errorf(
			"web_search: endpoints.%s: http is only allowed for loopback",
			provider)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// webSearchSourceLayer is the user-layer resource declaration the
// settings writer emits.
type webSearchSourceLayer struct {
	Kind     string            `json:"kind"`
	Impl     string            `json:"impl"`
	Settings WebSearchSettings `json:"settings"`
}

// webSearchLayer is the user-layer document SaveWebSearch writes.
type webSearchLayer struct {
	Version   string `json:"version"`
	Resources struct {
		ToolWebSearch *webSearchSourceLayer `json:"tool.websearch,omitempty"`
	} `json:"resources"`
}

// LoadWebSearch reads the user-layer tool.websearch settings. A
// missing layer or resource returns the zero value, which Resolve
// turns into the documented defaults.
func LoadWebSearch(configDir string) (WebSearchSettings, error) {
	path := filepath.Join(configDir, "opencraft.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WebSearchSettings{}, nil
		}
		return WebSearchSettings{}, err
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return WebSearchSettings{}, fmt.Errorf(
			"config: parse user config: %w", err)
	}
	raw, ok := doc.Resources["tool.websearch"]
	if !ok {
		return WebSearchSettings{}, nil
	}
	var source struct {
		Settings WebSearchSettings `json:"settings"`
	}
	if err := yaml.Unmarshal(raw, &source); err != nil {
		return WebSearchSettings{}, fmt.Errorf(
			"config: parse tool.websearch: %w", err)
	}
	return source.Settings, nil
}

// SaveWebSearch validates the settings and persists them as the user
// layer's tool.websearch resource. The resource is replaced wholesale,
// so clearing a key or an endpoint override takes effect; unrelated
// user-layer resources and deps survive.
func SaveWebSearch(configDir string, settings WebSearchSettings) error {
	if _, err := settings.Resolve(); err != nil {
		return err
	}
	layer := webSearchLayer{Version: "v1"}
	layer.Resources.ToolWebSearch = &webSearchSourceLayer{
		Kind:     "tool.Source",
		Impl:     "opencraft/websearch",
		Settings: settings,
	}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render websearch layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{"tool.websearch": true},
		nil,
		nil,
		false,
	)
	if err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(configDir, "opencraft.yaml"),
		merged,
		0o600,
	)
}
