package webfetch

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/opencraft/internal/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/utils/extract"
)

// Name is the canonical web_fetch tool name.
const Name = "web_fetch"

// Tool is the LLM-callable web_fetch tool. It retrieves an HTTP(S) URL
// and returns the extracted article content (title, description, site
// name, and cleaned text), bounded by timeout and size.
type Tool struct {
	extractor extract.Extractor
	gate      func(context.Context, string) error
}

// New creates the web_fetch tool.
func New() *Tool {
	return &Tool{extractor: extract.New()}
}

// SetGate installs the domain policy gate (nil disables it).
func (t *Tool) SetGate(gate func(context.Context, string) error) {
	t.gate = gate
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
	if t.gate != nil {
		if err := t.gate(ctx, u.Hostname()); err != nil {
			return "", err
		}
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

// DomainGate builds the URL host gate from the configured policy:
// deny/allow host lists plus an SSRF guard that blocks
// private/loopback/link-local destinations unless AllowPrivate is set.
func DomainGate(pol sandbox.WebFetchPolicy) func(context.Context, string) error {
	return func(ctx context.Context, host string) error {
		h := strings.ToLower(strings.TrimSpace(host))
		if h == "" {
			return errdefs.Validationf("web_fetch: empty host")
		}
		if hostMatches(h, pol.DenyHosts) {
			return errdefs.Validationf(
				"web_fetch: host %q is denied by policy", host)
		}
		if len(pol.AllowHosts) > 0 && !hostMatches(h, pol.AllowHosts) {
			return errdefs.Validationf(
				"web_fetch: host %q is not in the allow list", host)
		}
		if !pol.AllowPrivate {
			if err := checkPublicHost(ctx, h); err != nil {
				return err
			}
		}
		return nil
	}
}

// YOLOBypassGate wraps a domain gate so YOLO sessions skip it
// entirely: the mode disables the sandbox by definition, so a web_fetch
// that would be unrestricted at the network layer should not be
// re-gated by the tool. Sessions without a persisted mode keep the
// inner gate.
func YOLOBypassGate(
	store *ocsessions.Store,
	gate func(context.Context, string) error,
) func(context.Context, string) error {
	return func(ctx context.Context, host string) error {
		if store != nil && sandbox.IsYOLO(ctx, store) {
			return nil
		}
		return gate(ctx, host)
	}
}

// hostMatches matches exact hosts and "*.example.com" suffix patterns.
func hostMatches(host string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if p == host {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(host, p[1:]) {
			return true
		}
	}
	return false
}

// checkPublicHost rejects IP literals and resolved destinations in
// private/loopback/link-local space (SSRF guard).
func checkPublicHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return errdefs.Validationf(
				"web_fetch: private address %q is blocked by the SSRF guard", host)
		}
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(dctx, "ip", host)
	if err != nil {
		return errdefs.Validationf("web_fetch: resolve %q: %v", host, err)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return errdefs.Validationf(
				"web_fetch: host %q resolves to private address %s (blocked by the SSRF guard)",
				host, ip)
		}
	}
	return nil
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)
