// Package websearch implements opencraft's built-in web_search tool: a
// provider-independent web search that works with any chat model.
//
// The tool is discovery-only: it returns ranked results (title, url,
// snippet) and, for providers that hand back model-ready excerpts, one
// bounded context block. Reading a page in full stays with the
// web_fetch tool, which owns the SSRF guard and readability
// extraction. Providers are declared in the tool.websearch deployment
// settings; the keyless Parallel/Exa hosted MCP endpoints make the
// tool usable out of the box, while Tavily/Brave take a user API key
// for stable quotas.
//
// The settings shape, defaults, validation and keyring references live
// in foundation/config, so the settings page, the user-layer writer
// and this tool cannot drift.
package websearch

import occonfig "github.com/GizClaw/opencraft/internal/foundation/config"

type (
	// Settings is the tool.websearch deployment settings subtree.
	Settings = occonfig.WebSearchSettings
	// APIKeys carries one optional credential per provider.
	APIKeys = occonfig.WebSearchAPIKeys
	// Endpoints overrides provider base URLs.
	Endpoints = occonfig.WebSearchEndpoints
	// resolved is the validated, defaulted view the tool uses.
	resolved = occonfig.WebSearchConfig
)

// Provider ids accepted by the provider setting.
const (
	ProviderAuto     = occonfig.WebSearchProviderAuto
	ProviderExa      = occonfig.WebSearchProviderExa
	ProviderParallel = occonfig.WebSearchProviderParallel
	ProviderTavily   = occonfig.WebSearchProviderTavily
	ProviderBrave    = occonfig.WebSearchProviderBrave

	// Defaults and bounds live in foundation/config; the local aliases
	// keep the tool's own tests and messages readable.
	DefaultMaxResults = occonfig.WebSearchDefaultMaxResults
	MinMaxResults     = occonfig.WebSearchMinMaxResults
	MaxMaxResults     = occonfig.WebSearchMaxMaxResults

	DefaultTimeout = occonfig.WebSearchDefaultTimeout
	MinTimeout     = occonfig.WebSearchMinTimeout
	MaxTimeout     = occonfig.WebSearchMaxTimeout

	// MaxQueryRunes bounds the model-supplied query; MaxDomains bounds
	// the optional domain filter.
	MaxQueryRunes = 512
	MaxDomains    = 5

	// MaxSnippetRunes bounds one result snippet; MaxContextRunes
	// bounds the optional model-ready excerpt block. Both keep a
	// search result well inside the tool result budget.
	MaxSnippetRunes = 400
	MaxContextRunes = 8000
)

// keylessProviders lists the providers the auto mode may pick without
// any user credential.
var keylessProviders = occonfig.WebSearchKeylessProviders()
