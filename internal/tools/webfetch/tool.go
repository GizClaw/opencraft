package webfetch

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/opencraft/internal/utils/extract"
)

// Name is the canonical web_fetch tool name.
const Name = "web_fetch"

// Tool is the LLM-callable web_fetch tool. It retrieves an HTTP(S) URL
// and returns the extracted article content (title, description, site
// name, and cleaned text), bounded by timeout and size.
type Tool struct {
	extractor extract.Extractor
}

// New creates the web_fetch tool.
func New() *Tool {
	return &Tool{extractor: extract.New()}
}

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Fetch an HTTP(S) URL and return the extracted article content: "+
			"title, description, site name, and cleaned main text "+
			"(readability + metadata, whitespace-collapsed). Use for pages "+
			"a search result points to, documentation, or any URL whose "+
			"content you need. Non-http(s) schemes are rejected; response "+
			"size and runtime are bounded.",
		message.ToolProperty("url", "string",
			"The absolute http:// or https:// URL to fetch (required)."),
		message.ToolPropertyWithDefault("max_length", "integer",
			"Maximum characters of text to return (default 8000).",
			8000),
		message.ToolPropertyWithDefault("format", "string",
			"Output format: \"text\" (default) or \"markdown\".",
			"text"),
	).Required("url").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
		Format    string `json:"format"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("web_fetch: parse arguments: %v", err)
	}
	if args.URL == "" {
		return "", errdefs.Validationf("web_fetch: url is required")
	}
	u, err := url.Parse(args.URL)
	if err != nil {
		return "", errdefs.Validationf("web_fetch: parse url: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errdefs.Validationf(
			"web_fetch: unsupported scheme %q", u.Scheme)
	}

	var opts []extract.Option
	if args.MaxLength > 0 {
		opts = append(opts, extract.WithMaxCharacters(args.MaxLength))
	}
	if strings.EqualFold(args.Format, "markdown") {
		opts = append(opts, extract.WithFormat(extract.FormatMarkdown))
	}

	content, err := t.extractor.Extract(ctx, args.URL, opts...)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"url":          content.FinalURL,
		"title":        content.Title,
		"description":  content.Description,
		"site_name":    content.SiteName,
		"content":      content.Content,
		"content_type": content.ContentType,
		"truncated":    content.Truncated,
	})
	if err != nil {
		return "", errdefs.Internalf("web_fetch: encode result: %v", err)
	}
	return string(payload), nil
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)
