package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// tavilyProvider calls Tavily's REST search API with the user's own
// key (header auth, so the key never enters a URL).
type tavilyProvider struct {
	client   *httpClient
	endpoint string
	apiKey   string
}

func (p *tavilyProvider) Name() string { return ProviderTavily }

func (p *tavilyProvider) Search(
	ctx context.Context,
	req Request,
) (Response, error) {
	body := map[string]any{
		"query":                  req.Query,
		"max_results":            req.Count,
		"search_depth":           "basic",
		"topic":                  "general",
		"include_published_date": true,
	}
	if req.Freshness != "" {
		body["time_range"] = req.Freshness
	}
	if len(req.Domains) > 0 {
		body["include_domains"] = req.Domains
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, errdefs.Internalf(
			"web_search: encode tavily request: %v", err)
	}
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, errdefs.Internalf(
			"web_search: build tavily request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	data, err := p.client.do(ctx, ProviderTavily, httpReq)
	if err != nil {
		return Response{}, err
	}
	var out struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Response{}, errdefs.NotAvailablef(
			"web_search: tavily returned an unexpected payload: %v", err)
	}
	results := make([]Result, 0, len(out.Results))
	for _, hit := range out.Results {
		if !isHTTPURL(hit.URL) {
			continue
		}
		results = append(results, Result{
			Title:     cleanSpace(hit.Title),
			URL:       hit.URL,
			Snippet:   truncateRunes(cleanSpace(hit.Content), MaxSnippetRunes),
			Published: normalizeUnknown(cleanSpace(hit.PublishedDate)),
		})
		if len(results) == req.Count {
			break
		}
	}
	return Response{Results: results}, nil
}

// braveFreshness maps the tool's freshness vocabulary onto Brave's
// two-letter codes. Unknown values were rejected by the tool layer.
func braveFreshness(freshness string) string {
	switch freshness {
	case "day":
		return "pd"
	case "week":
		return "pw"
	case "month":
		return "pm"
	case "year":
		return "py"
	default:
		return ""
	}
}
