package websearch

import (
	"context"
	"net/url"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// exaProvider calls Exa's hosted MCP server. The endpoint is keyless;
// an optional API key travels as the vendor's exaApiKey query
// parameter (their documented MCP convention), which is why this
// package never logs request URLs.
type exaProvider struct {
	client   *mcpClient
	endpoint string
	apiKey   string
}

func (p *exaProvider) Name() string { return ProviderExa }

func (p *exaProvider) Search(
	ctx context.Context,
	req Request,
) (Response, error) {
	endpoint := p.endpoint
	if p.apiKey != "" {
		u, err := url.Parse(endpoint)
		if err != nil {
			return Response{}, errdefs.Internalf(
				"web_search: parse exa endpoint: %v", err)
		}
		q := u.Query()
		q.Set("exaApiKey", p.apiKey)
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}
	text, err := p.client.call(ctx, endpoint, nil, "web_search_exa",
		map[string]any{
			"query":      req.Query,
			"type":       "auto",
			"numResults": req.Count,
			"livecrawl":  "fallback",
		})
	if err != nil {
		return Response{}, err
	}
	resp := parseExaText(text, req.Count)
	if len(resp.Results) == 0 {
		// Exa reports free-tier exhaustion as an ordinary text payload
		// on a 200 response ("You've hit Exa's free MCP rate limit...").
		// Read as content it becomes a zero-result answer that hides the
		// real failure and blocks the auto fallback, so surface it as
		// the rate-limit error it is.
		if err := exaNoticeError(text); err != nil {
			return Response{}, err
		}
	}
	return resp, nil
}

// exaNoticeError maps Exa's out-of-band notices onto typed errors.
func exaNoticeError(text string) error {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") {
		return errdefs.RateLimitf(
			"web_search: exa free MCP rate limit reached; " +
				"add an Exa API key in Settings > Tools > Web search " +
				"or pick another provider")
	}
	return nil
}

// parseExaText splits Exa's model-ready text output into result links
// plus one context block. Each hit is a "Title/URL/Published/Author/
// Highlights" section separated by a "---" line.
func parseExaText(text string, count int) Response {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	normalized := strings.ReplaceAll(text, "\n---\n", "\n\n---\n\n")
	var results []Result
	for _, block := range strings.Split(normalized, "\n---\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		hit, ok := parseExaBlock(block)
		if !ok {
			continue
		}
		results = append(results, hit)
		if len(results) == count {
			break
		}
	}
	context := strings.TrimSpace(normalized)
	if context == "" {
		return Response{Results: results}
	}
	return Response{
		Results: results,
		Context: truncateRunes(context, MaxContextRunes),
	}
}

// parseExaBlock reads one section. Only blocks with a usable http(s)
// URL are returned, so a malformed section degrades to "missing hit"
// instead of a broken link.
func parseExaBlock(block string) (Result, bool) {
	var out Result
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Title:"):
			out.Title = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
		case strings.HasPrefix(line, "URL:"):
			out.URL = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		case strings.HasPrefix(line, "Published:"):
			out.Published = normalizeUnknown(
				strings.TrimSpace(strings.TrimPrefix(line, "Published:")))
		case line == "Highlights:":
			// The body follows; the context block carries it.
		}
	}
	if !isHTTPURL(out.URL) {
		return Result{}, false
	}
	return out, true
}

// normalizeUnknown maps provider placeholders onto the empty string so
// the envelope stays free of "N/A" noise.
func normalizeUnknown(v string) string {
	if strings.EqualFold(v, "N/A") || v == "-" {
		return ""
	}
	return v
}
