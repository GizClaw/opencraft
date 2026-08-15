package webfetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolExtractsArticle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><head>
			<title>FlowCraft SDK</title>
			<meta name="description" content="Go toolkit for AI agents">
			<meta property="og:site_name" content="GitHub">
			</head><body><h1>FlowCraft</h1>
			<p>Production-grade Go SDK for building AI agents with long-term memory.</p>
			<script>alert("x")</script></body></html>`))
	}))
	defer srv.Close()

	out, err := New().Execute(context.Background(),
		`{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Description string `json:"description"`
		SiteName    string `json:"site_name"`
		Content     string `json:"content"`
		Truncated   bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse result: %v\n%s", err, out)
	}
	if !strings.Contains(res.Title, "FlowCraft") {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Description, "Go toolkit") {
		t.Errorf("description = %q", res.Description)
	}
	if res.SiteName == "" {
		t.Error("site_name empty")
	}
	if !strings.Contains(res.Content, "Production-grade Go SDK") {
		t.Errorf("content = %q", res.Content)
	}
	if strings.Contains(res.Content, "alert") {
		t.Errorf("script content leaked: %q", res.Content)
	}
	if res.Truncated {
		t.Error("unexpected truncation")
	}
}

func TestToolTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><p>" + strings.Repeat("x", 500) + "</p></body></html>"))
	}))
	defer srv.Close()

	out, err := New().Execute(context.Background(),
		`{"url":"`+srv.URL+`","max_length":50}`)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("expected truncation")
	}
	if len([]rune(res.Content)) > 50 {
		t.Errorf("content too long: %d", len([]rune(res.Content)))
	}
}

func TestToolRejectsUnsupportedScheme(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x"} {
		if _, err := New().Execute(context.Background(),
			`{"url":"`+u+`"}`); err == nil {
			t.Errorf("Execute(%q) unexpectedly succeeded", u)
		}
	}
}

func TestToolRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := New().Execute(context.Background(),
		`{"url":"`+srv.URL+`"}`); err == nil {
		t.Fatal("Execute unexpectedly succeeded on 404")
	}
}

func TestToolDefinition(t *testing.T) {
	tool := New()
	def := tool.Definition()
	if def.Name != Name || !strings.Contains(def.Description, "readability") {
		t.Fatalf("definition = %+v", def)
	}
	if tool.Metadata().MutatesState {
		t.Fatal("web_fetch must be read-only")
	}
}
