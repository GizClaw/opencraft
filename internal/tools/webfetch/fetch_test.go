package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchExtractsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<!doctype html><html><head><title>FlowCraft</title></head>
			<body><h1>Hello</h1><p>Some <b>visible</b> text.</p>
			<script>alert("nope")</script></body></html>`))
	}))
	defer srv.Close()

	got, err := (&Client{}).Fetch(context.Background(), srv.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Title: FlowCraft", "Hello", "Some visible text."} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "alert") {
		t.Errorf("script content leaked: %q", got)
	}
}

func TestFetchTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<p>" + strings.Repeat("x", 200) + "</p>"))
	}))
	defer srv.Close()

	got, err := (&Client{}).Fetch(context.Background(), srv.URL, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got)) != 63 { // 50 + "\n…(truncated)"
		t.Errorf("length = %d, want 63", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("missing truncation marker: %q", got)
	}
}

func TestFetchRejectsUnsupportedScheme(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "javascript:alert(1)"} {
		if _, err := (&Client{}).Fetch(context.Background(), u, 0); err == nil {
			t.Errorf("Fetch(%q) unexpectedly succeeded", u)
		}
	}
}

func TestFetchRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := (&Client{}).Fetch(context.Background(), srv.URL, 0); err == nil {
		t.Fatal("Fetch unexpectedly succeeded on 404")
	}
}

func TestToolDefinition(t *testing.T) {
	tool := New()
	def := tool.Definition()
	if def.Name != Name || !strings.Contains(def.Description, "http") {
		t.Fatalf("definition = %+v", def)
	}
}
