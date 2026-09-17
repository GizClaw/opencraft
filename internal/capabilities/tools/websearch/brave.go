package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// braveProvider calls Brave's Web Search API with the user's own key
// (X-Subscription-Token header).
type braveProvider struct {
	client   *httpClient
	endpoint string
	apiKey   string
}

func (p *braveProvider) Name() string { return ProviderBrave }

func (p *braveProvider) Search(
	ctx context.Context,
	req Request,
) (Response, error) {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return Response{}, errdefs.Internalf(
			"web_search: parse brave endpoint: %v", err)
	}
	q := u.Query()
	q.Set("q", withSiteFilter(req.Query, req.Domains))
	q.Set("count", strconv.Itoa(req.Count))
	if f := braveFreshness(req.Freshness); f != "" {
		q.Set("freshness", f)
	}
	u.RawQuery = q.Encode()
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Response{}, errdefs.Internalf(
			"web_search: build brave request: %v", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Subscription-Token", p.apiKey)
	data, err := p.client.do(ctx, ProviderBrave, httpReq)
	if err != nil {
		return Response{}, err
	}
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Response{}, errdefs.NotAvailablef(
			"web_search: brave returned an unexpected payload: %v", err)
	}
	results := make([]Result, 0, len(out.Web.Results))
	for _, hit := range out.Web.Results {
		if !isHTTPURL(hit.URL) {
			continue
		}
		results = append(results, Result{
			Title:     cleanSpace(hit.Title),
			URL:       hit.URL,
			Snippet:   truncateRunes(cleanSpace(hit.Description), MaxSnippetRunes),
			Published: normalizeUnknown(cleanSpace(hit.Age)),
		})
		if len(results) == req.Count {
			break
		}
	}
	return Response{Results: results}, nil
}

// withSiteFilter folds an optional domain list into the query using
// the site: operator Brave supports, so the caller's domain
// restriction survives without a separate API parameter.
func withSiteFilter(query string, domains []string) string {
	if len(domains) == 0 {
		return query
	}
	clause := "("
	for i, d := range domains {
		if i > 0 {
			clause += " OR "
		}
		clause += "site:" + d
	}
	clause += ")"
	return clause + " " + query
}
