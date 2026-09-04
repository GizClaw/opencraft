package extract

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// Thresholds aligned with the original summarize constants.
const (
	MinReadabilityChars = 200
	MinContentChars     = 200
	MinDescriptionChars = 120
	RelativeThreshold   = 0.6
)

// DefaultExtractor is the main implementation of the Extractor
// interface. It extracts HTML articles with a layered fallback chain:
// readability segments, readability text, then metadata description.
type DefaultExtractor struct {
	config *extractorConfig
}

// Extract routes to the HTML extraction path.
func (e *DefaultExtractor) Extract(ctx context.Context, url string, opts ...Option) (*ExtractedContent, error) {
	cfg := *e.config
	for _, opt := range opts {
		opt(&cfg)
	}

	diag := &Diagnostics{}
	return e.extractHTML(ctx, &cfg, url, diag)
}

// -------------------------------------------------------------------
// HTML extraction (3-layer fallback chain)
// -------------------------------------------------------------------

func (e *DefaultExtractor) extractHTML(ctx context.Context, cfg *extractorConfig, url string, diag *Diagnostics) (*ExtractedContent, error) {
	fetchStart := time.Now()
	fetchResult, err := Fetch(ctx, cfg.httpClient, cfg.timeout, cfg.userAgent, url)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(fetchResult.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	diag.Notes = fmt.Sprintf("fetch=%v", time.Since(fetchStart).Truncate(time.Millisecond))

	// Anti-bot detection (combined conditions: >=2 patterns AND short content).
	if blocked, pattern := DetectBlocking(bytes.NewReader(data)); blocked {
		diag.AttemptedSources = append(diag.AttemptedSources, "blocked:"+pattern)
		return e.handleBlocked(cfg, data, url, diag)
	}

	// Extract metadata with hostname fallback for SiteName.
	metadata, _ := ExtractMetadataWithURL(bytes.NewReader(data), fetchResult.FinalURL)

	// Readability: compute once, reuse across layers.
	readabilityResult, err := ExtractWithReadability(data)
	if err != nil {
		telemetry.WarnErr(ctx, "extract: readability extraction failed", err)
	}

	// --- Layer 1: Readability HTML Segments vs Raw Segments ---
	var readabilitySegments string
	if readabilityResult != nil && readabilityResult.HTML != "" {
		segs, err := ExtractArticleContent([]byte(readabilityResult.HTML), 30)
		if err == nil {
			readabilitySegments = segs
		} else {
			telemetry.WarnErr(ctx, "extract: readability article extraction failed",
				err)
		}
	}

	rawSegments, err := ExtractArticleContent(data, 30)
	if err != nil {
		telemetry.WarnErr(ctx, "extract: raw article extraction failed", err)
	}

	selectedContent := selectLayer1(readabilitySegments, rawSegments)
	selectedMethod := "segments"
	if selectedContent == readabilitySegments && readabilitySegments != "" {
		selectedMethod = "readability_segments"
	}

	// --- Layer 2: Readability text vs selected segments ---
	if readabilityResult != nil && readabilityResult.Text != "" {
		readText := readabilityResult.Text
		if len([]rune(readText)) >= MinReadabilityChars {
			if len([]rune(selectedContent)) < MinContentChars ||
				float64(len([]rune(readText))) >= float64(len([]rune(selectedContent)))*RelativeThreshold {
				selectedContent = readText
				selectedMethod = "readability"
			}
		}
	}

	// --- Layer 3: Description vs selected content ---
	description := ""
	title := ""
	siteName := ""
	if metadata != nil {
		description = metadata.Description
		title = metadata.Title
		siteName = metadata.SiteName
	}
	if readabilityResult != nil && readabilityResult.Title != "" {
		title = readabilityResult.Title
	}

	if len([]rune(description)) >= MinDescriptionChars &&
		(len([]rune(selectedContent)) < MinContentChars ||
			float64(len([]rune(description))) >= float64(len([]rune(selectedContent)))*RelativeThreshold) {
		selectedContent = description
		selectedMethod = "description"
	}

	if selectedContent == "" {
		return nil, fmt.Errorf("all extraction layers failed for %s", redactURL(url))
	}

	diag.Strategy = selectedMethod
	selectedContent = stripLeadingTitle(selectedContent, title)

	result := e.finalize(cfg, url, fetchResult.FinalURL, selectedContent, title, siteName, ContentArticle, diag)
	result.Description = description
	if metadata != nil {
		result.Metadata = metadata.ToMap()
		result.SiteName = siteName
	}
	return result, nil
}

// handleBlocked recovers article text via readability when the page looks
// like an anti-bot block page.
func (e *DefaultExtractor) handleBlocked(cfg *extractorConfig, data []byte, url string, diag *Diagnostics) (*ExtractedContent, error) {
	readResult, err := ExtractWithReadability(data)
	if err == nil && readResult != nil && len([]rune(readResult.Text)) >= MinReadabilityChars {
		diag.Strategy = "readability"
		diag.Notes += "; readability recovered from blocked page"
		return e.finalize(cfg, url, url, readResult.Text, readResult.Title, "", ContentArticle, diag), nil
	}
	return nil, fmt.Errorf("page appears blocked by anti-bot protection")
}

func selectLayer1(readabilitySegments, rawSegments string) string {
	rLen := len([]rune(readabilitySegments))
	sLen := len([]rune(rawSegments))

	if rLen >= MinReadabilityChars &&
		(sLen < MinContentChars || float64(rLen) >= float64(sLen)*RelativeThreshold) {
		return readabilitySegments
	}
	return rawSegments
}

// -------------------------------------------------------------------
// Post-processing helpers
// -------------------------------------------------------------------

func (e *DefaultExtractor) finalize(cfg *extractorConfig, url, finalURL, content, title, siteName string, ct ContentType, diag *Diagnostics) *ExtractedContent {
	finalContent := content
	if cfg.format == FormatMarkdown {
		finalContent = toMarkdown(finalContent, title)
	}

	cleaned := CleanString(finalContent, cfg.maxCharacters)

	return &ExtractedContent{
		URL:             url,
		FinalURL:        finalURL,
		Title:           title,
		SiteName:        siteName,
		Content:         cleaned.Text,
		ContentType:     ct,
		TotalCharacters: cleaned.TotalCharacters,
		WordCount:       countWords(cleaned.Text),
		Truncated:       cleaned.WasTruncated,
		Diagnostics:     diag,
	}
}

// toMarkdown wraps plain text content with a Markdown title header.
func toMarkdown(content, title string) string {
	if title == "" {
		return content
	}
	return "# " + title + "\n\n" + content
}

func stripLeadingTitle(content, title string) string {
	if title == "" || content == "" {
		return content
	}

	titleNorm := normalizeForComparison(title)
	contentNorm := normalizeForComparison(content)

	if len(contentNorm) <= len(titleNorm) {
		return content
	}

	prefix := contentNorm
	if len(prefix) > 300 {
		prefix = prefix[:300]
	}

	if strings.HasPrefix(prefix, titleNorm) {
		titleRuneLen := len([]rune(titleNorm))
		contentRunes := []rune(content)
		titleEnd := titleRuneLen
		for titleEnd < len(contentRunes) {
			r := contentRunes[titleEnd]
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r >= 0x80 {
				break
			}
			titleEnd++
		}
		if titleEnd < len(contentRunes) {
			return strings.TrimSpace(string(contentRunes[titleEnd:]))
		}
	}

	for _, sep := range []string{" - ", " | ", " — ", ": ", "\n"} {
		check := titleNorm + normalizeForComparison(sep)
		if strings.HasPrefix(prefix, check) {
			checkRuneLen := len([]rune(check))
			contentRunes := []rune(content)
			if checkRuneLen < len(contentRunes) {
				return strings.TrimSpace(string(contentRunes[checkRuneLen:]))
			}
		}
	}

	return content
}

func normalizeForComparison(s string) string {
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}
