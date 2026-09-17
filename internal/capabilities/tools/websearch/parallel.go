package websearch

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// parallelProvider calls Parallel's hosted Search MCP. The endpoint is
// keyless (their documented free tier); an optional API key travels as
// a Bearer token for higher rate limits.
type parallelProvider struct {
	client   *mcpClient
	endpoint string
	apiKey   string
}

func (p *parallelProvider) Name() string { return ProviderParallel }

func (p *parallelProvider) Search(
	ctx context.Context,
	req Request,
) (Response, error) {
	args := map[string]any{
		"objective":      req.Query,
		"search_queries": []string{req.Query},
	}
	if req.SessionID != "" {
		args["session_id"] = req.SessionID
	}
	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}
	text, err := p.client.call(ctx, p.endpoint, headers, "web_search", args)
	if err != nil {
		return Response{}, err
	}
	return parseParallelText(text, req.Count)
}

// parallelPayload is the JSON document Parallel returns inside the MCP
// text block.
type parallelPayload struct {
	// Results is a pointer so a payload without the field (an error or
	// notice document) is distinguishable from a real empty result
	// set.
	Results *[]struct {
		URL         string   `json:"url"`
		Title       string   `json:"title"`
		PublishDate *string  `json:"publish_date"`
		Excerpts    []string `json:"excerpts"`
	} `json:"results"`
}

// parseParallelText decodes Parallel's structured payload into links
// plus a bounded context block assembled from the excerpts. A payload
// that does not decode is an error, never an empty result set.
func parseParallelText(text string, count int) (Response, error) {
	var payload parallelPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return Response{}, errdefs.NotAvailablef(
			"web_search: parallel returned an unexpected payload: %v", err)
	}
	if payload.Results == nil {
		return Response{}, errdefs.NotAvailablef(
			"web_search: parallel returned no results field")
	}
	var results []Result
	var context strings.Builder
	for _, hit := range *payload.Results {
		if !isHTTPURL(hit.URL) {
			continue
		}
		title := strings.TrimSpace(hit.Title)
		if title == "" {
			title = hit.URL
		}
		published := ""
		if hit.PublishDate != nil {
			published = normalizeUnknown(strings.TrimSpace(*hit.PublishDate))
		}
		results = append(results, Result{
			Title:     title,
			URL:       hit.URL,
			Published: published,
		})
		body := cleanSpace(strings.Join(hit.Excerpts, " "))
		if body != "" {
			if context.Len() > 0 {
				context.WriteString("\n\n")
			}
			context.WriteString("# " + title + "\n" + hit.URL + "\n" + body)
		}
		if len(results) == count {
			break
		}
	}
	resp := Response{Results: results}
	if context.Len() > 0 {
		resp.Context = truncateRunes(context.String(), MaxContextRunes)
	}
	return resp, nil
}
