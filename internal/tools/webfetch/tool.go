package webfetch

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/sdk/errdefs"
	"github.com/GizClaw/flowcraft/sdk/message"
	"github.com/GizClaw/flowcraft/sdk/tool"
)

// Name is the canonical web_fetch tool name.
const Name = "web_fetch"

// Tool is the LLM-callable web_fetch tool. It retrieves an HTTP(S) URL
// and returns its readable text content, bounded by timeout and size.
type Tool struct {
	client *Client
}

// New creates the web_fetch tool.
func New() *Tool {
	return &Tool{client: &Client{}}
}

// Definition implements tool.Tool.
func (t *Tool) Definition() message.Definition {
	return message.DefineSchema(
		Name,
		"Fetch an HTTP(S) URL and return its readable text content "+
			"(title plus visible page text, whitespace-collapsed). "+
			"Use for pages a search result points to, documentation, "+
			"or any URL whose content you need. Non-http(s) schemes are "+
			"rejected; response size and runtime are bounded.",
		message.ToolProperty("url", "string",
			"The absolute http:// or https:// URL to fetch (required)."),
		message.ToolPropertyWithDefault("max_length", "integer",
			"Maximum characters of text to return (default 8000).",
			8000),
	).Required("url").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("web_fetch: parse arguments: %v", err)
	}
	if args.URL == "" {
		return "", errdefs.Validationf("web_fetch: url is required")
	}
	text, err := t.client.Fetch(ctx, args.URL, args.MaxLength)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]string{
		"url":  args.URL,
		"text": text,
	})
	if err != nil {
		return "", errdefs.Internalf("web_fetch: encode result: %v", err)
	}
	return string(payload), nil
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)
