package extract

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultExtractorArticle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<!doctype html><html><head>
			<title>FlowCraft</title>
			<meta name="description" content="A Go toolkit for AI agents.">
			</head><body><article>
			<h1>Overview</h1>
			<p>FlowCraft is a modular Go toolkit for building AI applications.</p>
			<p>It provides long-term memory, provider integrations, and graphs.</p>
			</article></body></html>`))
	}))
	defer srv.Close()

	res, err := New().Extract(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Title, "FlowCraft") {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Description, "Go toolkit") {
		t.Errorf("description = %q", res.Description)
	}
	if !strings.Contains(res.Content, "modular Go toolkit") {
		t.Errorf("content = %q", res.Content)
	}
	if res.ContentType != ContentArticle {
		t.Errorf("content type = %q", res.ContentType)
	}
	if res.Diagnostics == nil || res.Diagnostics.Strategy == "" {
		t.Errorf("missing diagnostics: %+v", res.Diagnostics)
	}
	if res.TotalCharacters <= 0 {
		t.Errorf("total characters = %d", res.TotalCharacters)
	}
}

func TestDefaultExtractorMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Doc</title></head>
			<body><p>Body text.</p></body></html>`))
	}))
	defer srv.Close()

	res, err := New().Extract(context.Background(), srv.URL, WithFormat(FormatMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Content, "# Doc") {
		t.Errorf("markdown content = %q", res.Content)
	}
}

func TestDefaultExtractorRejectsBlockedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
			<p>Access denied. Enable JavaScript.</p>
			<p>Verify you are human. Cloudflare.</p>
			</body></html>`))
	}))
	defer srv.Close()

	if _, err := New().Extract(context.Background(), srv.URL); err == nil {
		t.Fatal("blocked page unexpectedly extracted")
	}
}

func TestMetadataOG(t *testing.T) {
	doc := []byte(`<html><head>
		<title>Page</title>
		<meta property="og:title" content="OG Title">
		<meta property="og:description" content="OG description text.">
		<meta property="og:site_name" content="Example">
		<meta property="og:image" content="https://example.com/i.png">
		</head><body></body></html>`)
	meta, jsonLd := ExtractMetadataWithURL(bytes.NewReader(doc), "https://example.com/x")
	if meta.Title != "OG Title" {
		t.Errorf("title = %q", meta.Title)
	}
	if meta.Description != "OG description text." {
		t.Errorf("description = %q", meta.Description)
	}
	if meta.SiteName != "Example" {
		t.Errorf("site name = %q", meta.SiteName)
	}
	if meta.Image == "" {
		t.Error("image empty")
	}
	if jsonLd != nil {
		t.Errorf("unexpected json-ld: %+v", jsonLd)
	}
}

func TestStripHiddenHTML(t *testing.T) {
	in := `<html><body>
		<p style="display:none">hidden</p>
		<p style="visibility:hidden">invisible</p>
		<p>visible</p>
		<script>bad()</script>
		</body></html>`
	out, err := StripHiddenHTML(strings.NewReader(in), DefaultSanitizeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hidden") || strings.Contains(out, "invisible") ||
		strings.Contains(out, "bad()") {
		t.Errorf("hidden content leaked: %q", out)
	}
	if !strings.Contains(out, "visible") {
		t.Errorf("visible content lost: %q", out)
	}
}

func TestApplyBudgetTruncatesAtBoundary(t *testing.T) {
	long := strings.Repeat("word ", 200)
	res := ApplyBudget(long, 100)
	if !res.WasTruncated {
		t.Fatal("expected truncation")
	}
	if len([]rune(res.Text)) > 100 {
		t.Errorf("truncated length = %d, want <= 100", len([]rune(res.Text)))
	}
	if res.TotalCharacters != len([]rune(long)) {
		t.Errorf("total = %d, want %d", res.TotalCharacters, len([]rune(long)))
	}
}

func TestFetchRejectsNonHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), nil, 0, "test", srv.URL); err == nil {
		t.Fatal("Fetch unexpectedly accepted PDF")
	}
}
