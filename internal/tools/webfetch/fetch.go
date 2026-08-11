// Package webfetch implements the opencraft web_fetch tool: HTTP(S)
// retrieval with HTML-to-text extraction, bounded by size and timeout.
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	defaultTimeout = 15 * time.Second
	maxBodyBytes   = 2 << 20 // 2 MiB
)

// Client fetches a URL and returns its readable text content.
type Client struct {
	HTTP *http.Client
}

// Fetch retrieves url and extracts title + visible text. maxLength caps
// the returned text in runes; zero uses 8000.
func (c *Client) Fetch(ctx context.Context, rawURL string, maxLength int) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("web_fetch: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web_fetch: unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("web_fetch: url has no host")
	}

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", "opencraft/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_fetch: status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("web_fetch: read body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return "", fmt.Errorf("%w: body exceeds %d bytes", errBodyTooLarge, maxBodyBytes)
	}

	text := ExtractText(body)
	if maxLength <= 0 {
		maxLength = 8000
	}
	runes := []rune(text)
	if len(runes) > maxLength {
		return string(runes[:maxLength]) + "\n…(truncated)", nil
	}
	return text, nil
}

// ExtractText converts HTML into plain text: the document title followed
// by the visible body text with whitespace collapsed.
func ExtractText(doc []byte) string {
	node, err := html.Parse(strings.NewReader(string(doc)))
	if err != nil {
		return strings.TrimSpace(string(doc))
	}
	var title string
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			text := collapseSpace(n.Data)
			if text == "" {
				break
			}
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteByte(' ')
			}
			sb.WriteString(text)
		case html.ElementNode:
			switch n.Data {
			case "script", "style", "noscript", "template", "svg":
				return
			case "head":
				var findTitle func(*html.Node)
				findTitle = func(h *html.Node) {
					if h.Type == html.ElementNode && h.Data == "title" &&
						h.FirstChild != nil && title == "" {
						title = collapseSpace(h.FirstChild.Data)
						return
					}
					for ch := h.FirstChild; ch != nil; ch = ch.NextSibling {
						findTitle(ch)
					}
				}
				findTitle(n)
				return
			default:
				if blockLevel[n.Data] && sb.Len() > 0 &&
					!strings.HasSuffix(sb.String(), "\n") {
					sb.WriteByte('\n')
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(node)
	text := strings.TrimSpace(sb.String())
	if title != "" {
		text = "Title: " + title + "\n" + text
	}
	return text
}

var blockLevel = map[string]bool{
	"p": true, "div": true, "li": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"br": true, "tr": true, "table": true, "section": true, "article": true,
	"pre": true, "blockquote": true,
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// IsTooLarge reports whether err came from the response size cap.
func IsTooLarge(err error) bool {
	return errors.Is(err, errBodyTooLarge)
}

var errBodyTooLarge = errors.New("body too large")
