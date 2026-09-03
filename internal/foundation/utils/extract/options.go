package extract

import (
	"net/http"
	"time"
)

// Format specifies the output format.
type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

type extractorConfig struct {
	httpClient    *http.Client
	timeout       time.Duration
	maxCharacters int
	format        Format
	userAgent     string
}

// Option configures an Extractor.
type Option func(*extractorConfig)

// WithMaxCharacters sets the maximum characters to extract.
// Pass 0 to disable truncation.
func WithMaxCharacters(n int) Option {
	return func(c *extractorConfig) {
		c.maxCharacters = n
	}
}

// WithFormat sets the output content format.
func WithFormat(f Format) Option {
	return func(c *extractorConfig) {
		c.format = f
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *extractorConfig) {
		cfg.httpClient = c
	}
}
