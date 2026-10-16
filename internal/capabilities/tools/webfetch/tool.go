package webfetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/netguard"
	"github.com/GizClaw/opencraft/internal/foundation/utils/extract"
)

// Name is the canonical web_fetch tool name.
const Name = "web_fetch"

// Tool is the LLM-callable web_fetch tool. It retrieves an HTTP(S) URL
// and returns the extracted article content (title, description, site
// name, and cleaned text), bounded by timeout and size.
type Tool struct {
	extractor extract.Extractor
	gate      func(context.Context, string) error
	// allowPrivate, when non-nil, is consulted per request to decide
	// whether private/loopback/link-local destinations are permitted
	// (YOLO sessions opt in). Nil means always blocked.
	allowPrivate func(context.Context) bool
	// client is the hardened HTTP client used when a gate is wired: it
	// re-validates every redirect target and pins DNS to the resolved
	// addresses so the SSRF guard cannot be bypassed with a redirect
	// or a DNS rebinding between validation and dial.
	client *http.Client
}

// New creates the web_fetch tool.
func New() *Tool {
	return &Tool{extractor: extract.New()}
}

// SetGate installs the domain policy gate (nil disables it).
func (t *Tool) SetGate(gate func(context.Context, string) error) {
	t.gate = gate
	t.rebuildClient()
}

// SetAllowPrivate installs the per-request resolver that permits
// private destinations. It is consulted only when a gate is wired;
// nil (the default) keeps private destinations blocked.
func (t *Tool) SetAllowPrivate(fn func(context.Context) bool) {
	t.allowPrivate = fn
	t.rebuildClient()
}

func (t *Tool) rebuildClient() {
	if t.gate != nil {
		t.client = hardenedClient(t.gate, t.allowPrivate)
	} else {
		t.client = nil
	}
}

// SetSSRFGuard installs the hardened client without a domain gate: the
// fallback when a deployment carries no net policy resource. No host
// list applies (there is none to enforce), but the transport still
// refuses private/loopback/link-local destinations, pins DNS to the
// validated addresses and revalidates every redirect hop, so a missing
// policy resource cannot widen the SSRF surface.
func (t *Tool) SetSSRFGuard(allowPrivate func(context.Context) bool) {
	t.gate = func(context.Context, string) error { return nil }
	t.allowPrivate = allowPrivate
	t.rebuildClient()
}

// clampMaxLength caps one caller-supplied max_length: 0 and negative
// values pass through (the extractor's default applies), anything
// above the ceiling is clamped so a single fetch cannot flood the
// model context with an arbitrarily large result.
func clampMaxLength(n int) int {
	if n > maxLengthCeiling {
		return maxLengthCeiling
	}
	return n
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
			"Maximum characters of text to return (default 8000, capped at 100000).",
			8000),
		message.ToolPropertyWithDefault("format", "string",
			"Output format: \"text\" (default) or \"markdown\".",
			"text"),
	).Required("url").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *Tool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *Tool) execute(ctx context.Context, arguments string) (string, error) {
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
		// The SSRF guard pins DNS to the validated addresses only on
		// direct dials; through an HTTP(S) proxy the target would be
		// re-resolved on the proxy side. Fail closed before doing any
		// work — netguard.Client applies the same rule to every
		// request, so a redirect hop cannot slip past this check.
		// YOLO/allow_private paths are exempt (no SSRF concern).
		if err := netguard.RefuseProxy(Name, u.String(),
			t.allowPrivate != nil && t.allowPrivate(ctx)); err != nil {
			return "", err
		}
	}

	var opts []extract.Option
	if args.MaxLength > 0 {
		opts = append(opts, extract.WithMaxCharacters(clampMaxLength(args.MaxLength)))
	}
	if strings.EqualFold(args.Format, "markdown") {
		opts = append(opts, extract.WithFormat(extract.FormatMarkdown))
	}
	if t.client != nil {
		opts = append(opts, extract.WithHTTPClient(t.client))
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

// maxRedirects bounds redirect chains followed by the hardened client.
const maxRedirects = 10

// clientTimeout bounds one whole fetch through the hardened client.
const clientTimeout = 30 * time.Second

// maxLengthCeiling bounds the max_length argument. It is the
// extractor's own default cap, so a caller asking for more than the
// default path would return cannot make the result bigger than any
// default fetch.
const maxLengthCeiling = 100_000

// hardenedClient builds the tool's http.Client: the shared netguard
// transport, which validates the destination host on every connection,
// re-runs the gate for each redirect hop, resolves DNS once per dial,
// and connects to exactly the validated addresses — closing the
// redirect and DNS-rebinding bypasses of a check that only runs on the
// original URL. The same client backs the provider artifact downloads
// in generate_video (internal/foundation/netguard).
func hardenedClient(
	gate func(context.Context, string) error,
	allowPrivate func(context.Context) bool,
) *http.Client {
	return netguard.Client(netguard.Options{
		Label:        Name,
		Gate:         gate,
		AllowPrivate: allowPrivate,
		Timeout:      clientTimeout,
		MaxRedirects: maxRedirects,
	})
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
	return netguard.ResolvePublic(ctx, Name, host)
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)
