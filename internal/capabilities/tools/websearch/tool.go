package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
)

// Name is the canonical web_search tool name.
const Name = "web_search"

// noResultsNote replaces an empty result list with an explicit hint so
// the model retries with a different query instead of treating the
// empty array as a failure.
const noResultsNote = "no results; try a different query"

// Tool is the LLM-callable web_search tool. It is stateless apart from
// the auto-selection counter, so one instance serves every session.
type Tool struct {
	settings  resolved
	providers map[string]Provider
	autoOrder []string
	counter   atomic.Uint64
}

var _ tool.Tool = (*Tool)(nil)

// New builds the tool from the tool.websearch settings. Providers that
// need a credential are only constructed when one is configured; the
// keyless hosted MCP backends are always available.
func New(settings Settings) (*Tool, error) {
	cfg, err := settings.Resolve()
	if err != nil {
		return nil, err
	}
	client := newHTTPClient()
	mcp := &mcpClient{http: client}
	providers := map[string]Provider{
		ProviderExa: &exaProvider{
			client:   mcp,
			endpoint: cfg.Endpoints[ProviderExa],
			apiKey:   cfg.APIKeys.Get(ProviderExa),
		},
		ProviderParallel: &parallelProvider{
			client:   mcp,
			endpoint: cfg.Endpoints[ProviderParallel],
			apiKey:   cfg.APIKeys.Get(ProviderParallel),
		},
	}
	if key := cfg.APIKeys.Get(ProviderTavily); key != "" {
		providers[ProviderTavily] = &tavilyProvider{
			client:   client,
			endpoint: cfg.Endpoints[ProviderTavily],
			apiKey:   key,
		}
	}
	if key := cfg.APIKeys.Get(ProviderBrave); key != "" {
		providers[ProviderBrave] = &braveProvider{
			client:   client,
			endpoint: cfg.Endpoints[ProviderBrave],
			apiKey:   key,
		}
	}
	auto := make([]string, 0, len(keylessProviders))
	for _, name := range keylessProviders {
		if _, ok := providers[name]; ok {
			auto = append(auto, name)
		}
	}
	return &Tool{
		settings:  cfg,
		providers: providers,
		autoOrder: auto,
	}, nil
}

// Definition implements tool.Tool. The tool stays discoverable through
// tool_search: the description carries the words users phrase search
// requests with, because the discovery index is BM25 over name and
// description only.
func (t *Tool) Definition() message.ToolDefinition {
	description := "Search the web for current information, news, " +
		"documentation, or facts beyond the model's knowledge cutoff. " +
		"Returns ranked results with titles and URLs, and a short " +
		"extracted context for backends that provide one. Use it " +
		"whenever a question needs up-to-date or sourced information, " +
		"then pass a promising URL to web_fetch to read that page in " +
		"full. The search backend is chosen by the host; the query is " +
		"sent to the configured search provider."
	return message.DefineSchema(
		Name,
		description,
		message.ToolProperty("query", "string",
			"The search query (required)."),
		message.ToolPropertyWithDefault("count", "integer",
			fmt.Sprintf(
				"Maximum number of results (%d-%d, default %d).",
				MinMaxResults, MaxMaxResults, t.settings.MaxResults),
			t.settings.MaxResults),
		message.ToolEnumProperty("freshness", "string",
			"Optional recency filter; backends that cannot honor it "+
				"ignore it.",
			"day", "week", "month", "year"),
		message.ToolArrayProperty("domains",
			"Optional list of domains to restrict or prefer, at most "+
				"5 entries; backends that cannot honor it ignore it.",
			message.Items("string")),
	).Required("query").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.Tool: the tool bounds its own execution
// time, and search calls have no side effects.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{SelfTimeout: true}
}

// Execute implements tool.Tool. The result is one text part carrying
// the JSON envelope.
func (t *Tool) Execute(
	ctx context.Context,
	arguments string,
) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// toolArgs is the model-facing argument shape.
type toolArgs struct {
	Query     string   `json:"query"`
	Count     int      `json:"count"`
	Freshness string   `json:"freshness"`
	Domains   []string `json:"domains"`
}

// envelope is the tool result shape: links first, then the optional
// provider context, so the model can pick a URL for web_fetch and the
// UI can render the list without parsing provider text.
type envelope struct {
	Provider string   `json:"provider"`
	Query    string   `json:"query"`
	Results  []Result `json:"results"`
	Context  string   `json:"context,omitempty"`
	Note     string   `json:"note,omitempty"`
}

func (t *Tool) execute(
	ctx context.Context,
	arguments string,
) (string, error) {
	parsed, err := t.parseArgs(arguments)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, t.settings.Timeout)
	defer cancel()

	provider := t.pickProvider(ctx)
	resp, err := t.search(ctx, parsed, provider)
	if err != nil {
		next, ok := t.fallback(provider, err)
		if !ok {
			return "", err
		}
		alt, altErr := t.search(ctx, parsed, next)
		if altErr != nil {
			return "", err
		}
		resp, provider = alt, next
	}
	out := envelope{
		Provider: provider,
		Query:    parsed.Query,
		Results:  resp.Results,
		Context:  resp.Context,
	}
	if out.Results == nil {
		out.Results = []Result{}
	}
	if len(out.Results) == 0 && out.Context == "" {
		out.Note = noResultsNote
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", errdefs.Internalf("web_search: encode result: %v", err)
	}
	return string(data), nil
}

// parseArgs validates the model-supplied arguments.
func (t *Tool) parseArgs(arguments string) (toolArgs, error) {
	var raw toolArgs
	if err := json.Unmarshal([]byte(arguments), &raw); err != nil {
		return toolArgs{}, errdefs.Validationf(
			"web_search: parse arguments: %v", err)
	}
	out := toolArgs{
		Query:     strings.TrimSpace(raw.Query),
		Count:     raw.Count,
		Freshness: strings.ToLower(strings.TrimSpace(raw.Freshness)),
	}
	if out.Query == "" {
		return toolArgs{}, errdefs.Validationf(
			"web_search: query is required")
	}
	if len([]rune(out.Query)) > MaxQueryRunes {
		return toolArgs{}, errdefs.Validationf(
			"web_search: query exceeds %d characters", MaxQueryRunes)
	}
	switch out.Freshness {
	case "", "day", "week", "month", "year":
	default:
		return toolArgs{}, errdefs.Validationf(
			"web_search: unknown freshness %q "+
				"(want day, week, month or year)", out.Freshness)
	}
	if len(raw.Domains) > MaxDomains {
		return toolArgs{}, errdefs.Validationf(
			"web_search: at most %d domains are allowed", MaxDomains)
	}
	for _, d := range raw.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if !validDomain(d) {
			return toolArgs{}, errdefs.Validationf(
				"web_search: invalid domain %q", d)
		}
		out.Domains = append(out.Domains, d)
	}
	if out.Count <= 0 {
		out.Count = t.settings.MaxResults
	}
	if out.Count > t.settings.MaxResults {
		out.Count = t.settings.MaxResults
	}
	return out, nil
}

// validDomain accepts a bare hostname: labels of letters, digits and
// dashes separated by dots. Schemes, paths and wildcards are rejected
// so a domain filter cannot smuggle in a different URL.
func validDomain(domain string) bool {
	if len(domain) > 253 {
		return false
	}
	if strings.ContainsAny(domain, "/:@ ") {
		return false
	}
	for _, r := range domain {
		if r == '.' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

// pickProvider resolves the provider for one call. Explicit settings
// win; auto mode splits deterministically per conversation so related
// turns keep hitting the same backend, falling back to a counter when
// no run identity is available (tests, direct calls).
func (t *Tool) pickProvider(ctx context.Context) string {
	if t.settings.Provider != ProviderAuto {
		return t.settings.Provider
	}
	if len(t.autoOrder) == 0 {
		return ""
	}
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		if id := info.ConversationID; id != "" {
			h := fnv.New32a()
			_, _ = h.Write([]byte(id))
			return t.autoOrder[int(h.Sum32()%uint32(len(t.autoOrder)))]
		}
	}
	n := t.counter.Add(1)
	return t.autoOrder[int(n%uint64(len(t.autoOrder)))]
}

// fallback names the other keyless provider for one retry when auto
// mode's first backend fails. Cancellation and deadline errors end the
// call instead: retrying a different vendor after the shared deadline
// would just delay the failure.
func (t *Tool) fallback(current string, err error) (string, bool) {
	if err == nil || t.settings.Provider != ProviderAuto {
		return "", false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return "", false
	}
	for _, name := range t.autoOrder {
		if name == current {
			continue
		}
		if _, ok := t.providers[name]; ok {
			return name, true
		}
	}
	return "", false
}

// search performs one provider call, rejecting an unconfigured
// explicit provider with a validation error instead of a nil-provider
// panic.
func (t *Tool) search(
	ctx context.Context,
	args toolArgs,
	provider string,
) (Response, error) {
	p, ok := t.providers[provider]
	if !ok {
		return Response{}, errdefs.Validationf(
			"web_search: provider %q is not configured", provider)
	}
	return p.Search(ctx, Request{
		Query:     args.Query,
		Count:     args.Count,
		Freshness: args.Freshness,
		Domains:   args.Domains,
		SessionID: conversationID(ctx),
	})
}

// conversationID returns the stable per-conversation identifier used
// for provider-side rate-limit grouping, bounded to what the vendors
// accept.
func conversationID(ctx context.Context) string {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok {
		return ""
	}
	id := info.ConversationID
	if len(id) > 100 {
		id = id[:100]
	}
	return id
}
