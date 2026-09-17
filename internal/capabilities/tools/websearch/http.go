package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/version"
)

// maxResponseBytes bounds every provider response body. Search
// responses are small; anything larger is treated as an error rather
// than truncated silently.
const maxResponseBytes = 1 << 20

// httpClient is the shared, redirect-free client all providers use.
// Timeouts come from the per-call context (the tool owns the
// deadline), so the client itself has none.
type httpClient struct {
	client    *http.Client
	userAgent string
}

func newHTTPClient() *httpClient {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 10
	tr.MaxIdleConnsPerHost = 4
	tr.IdleConnTimeout = 90 * time.Second
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &httpClient{
		// Fixed endpoints come from the deployment settings, so
		// redirects are never needed; refusing them keeps a provider
		// from rerouting a request (or its Authorization header)
		// somewhere else.
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		userAgent: "OpenCraft/" + version.ServiceVersion,
	}
}

// do runs one request and returns the bounded body, mapping HTTP
// failures onto errdefs categories. provider names the backend in the
// error so the model (and the audit log) can tell them apart.
func (c *httpClient) do(
	ctx context.Context,
	provider string,
	req *http.Request,
) ([]byte, error) {
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.client.Do(req.WithContext(ctx))
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return nil, errdefs.Timeoutf(
				"web_search: %s request timed out: %w", provider, err)
		}
		return nil, errdefs.NotAvailablef(
			"web_search: %s request failed: %w", provider, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "web_search: close response body failed",
			resp.Body.Close())
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, errdefs.NotAvailablef(
			"web_search: read %s response: %w", provider, err)
	}
	if len(body) > maxResponseBytes {
		return nil, errdefs.Internalf(
			"web_search: %s response exceeded %d bytes",
			provider, maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(provider, resp, body)
	}
	return body, nil
}

// statusError maps a non-200 provider response. The body is only used
// to detect an explicitly rate-limited answer; it is never echoed into
// the error, so a provider that reflects credentials cannot leak them
// into the transcript or the audit log.
func statusError(provider string, resp *http.Response, body []byte) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden:
		return errdefs.Unauthorizedf(
			"web_search: %s rejected the request (%s); "+
				"check the configured API key", provider, resp.Status)
	case resp.StatusCode == http.StatusTooManyRequests:
		err := errdefs.RateLimitf(
			"web_search: %s rate limited the request (%s)",
			provider, resp.Status)
		if d := errdefs.ParseRetryAfter(
			resp.Header.Get("Retry-After")); d > 0 {
			err = errdefs.WithRetryAfter(err, d)
		}
		return err
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return errdefs.NotAvailablef(
			"web_search: %s tried to redirect (%s); "+
				"set a direct endpoint or remove the override",
			provider, resp.Status)
	case resp.StatusCode >= 500:
		return errdefs.NotAvailablef(
			"web_search: %s is unavailable (%s)", provider, resp.Status)
	default:
		return errdefs.Internalf(
			"web_search: %s rejected the request (%s)",
			provider, resp.Status)
	}
}
