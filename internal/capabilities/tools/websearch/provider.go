package websearch

import (
	"context"
	"net/url"
)

// Request is one normalized search request. Providers map the fields
// they support and ignore the rest; the tool documents that behavior
// in its schema so the model is not surprised.
type Request struct {
	// Query is the natural-language search query (required).
	Query string
	// Count is the maximum number of results the caller wants. The
	// tool clamps it into the configured range before calling.
	Count int
	// Freshness narrows the result window: "", "day", "week", "month"
	// or "year".
	Freshness string
	// Domains optionally restricts or prefers result domains (max 5).
	Domains []string
	// SessionID is a stable identifier for the conversation. Parallel
	// uses it for free-tier rate limiting and log correlation; other
	// providers ignore it.
	SessionID string
}

// Response is one provider's normalized answer. Results always carry
// links; Context, when set, is a bounded model-ready excerpt block
// (the provider's own extraction) and results then omit snippets to
// avoid duplication.
type Response struct {
	Results []Result
	Context string
}

// Result is one ranked search hit.
type Result struct {
	Title     string `json:"title,omitempty"`
	URL       string `json:"url"`
	Snippet   string `json:"snippet,omitempty"`
	Published string `json:"published,omitempty"`
}

// Provider is one search backend.
type Provider interface {
	Name() string
	Search(ctx context.Context, req Request) (Response, error)
}

// truncateRunes cuts s to at most max runes, appending a short marker
// when content was dropped. It never splits a rune.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// cleanSpace collapses whitespace runs into single spaces so provider
// text lands in the result envelope as one line.
func cleanSpace(s string) string {
	var b []rune
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
			if !space && len(b) > 0 {
				b = append(b, ' ')
			}
			space = true
		default:
			b = append(b, r)
			space = false
		}
	}
	out := string(b)
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return out
}

// isHTTPURL reports whether raw is an absolute http(s) URL. Providers
// use it to drop unusable hits instead of emitting broken links.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}
