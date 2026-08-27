package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/sandbox"
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

func TestToolGateBlocksPrivateHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be reached: private host blocked by the gate")
	}))
	defer srv.Close()

	tool := New()
	tool.SetGate(DomainGate(sandbox.WebFetchPolicy{}))
	_, err := tool.Execute(context.Background(), `{"url":"`+srv.URL+`"}`)
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("Execute(loopback) error = %v, want private-address rejection", err)
	}
}

func TestToolRedirectRevalidatesHost(t *testing.T) {
	blocked := "169.254.169.254"
	gate := func(_ context.Context, host string) error {
		if host == blocked {
			return errors.New("blocked by test gate")
		}
		return nil
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+blocked+"/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	tool := New()
	tool.SetGate(gate)
	// The initial server is loopback (httptest); allow private hosts so
	// the request reaches it, and rely on the redirect re-validation to
	// block the hostile hop.
	tool.SetAllowPrivate(func(context.Context) bool { return true })
	_, err := tool.Execute(context.Background(), `{"url":"`+srv.URL+`"}`)
	if err == nil || !strings.Contains(err.Error(), "blocked by test gate") {
		t.Fatalf("Execute(redirect) error = %v, want redirect re-validation failure", err)
	}
}

func TestToolDialResolvesOnceAndBlocksPrivate(t *testing.T) {
	orig := lookupHost
	lookupHost = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "example.test" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return nil, errors.New("unexpected host " + host)
	}
	t.Cleanup(func() { lookupHost = orig })

	tool := New()
	tool.SetGate(DomainGate(sandbox.WebFetchPolicy{}))
	_, err := tool.Execute(context.Background(), `{"url":"http://example.test/"}`)
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("Execute(dns) error = %v, want private-address rejection", err)
	}
}

func TestHardenedClientRedirectPolicy(t *testing.T) {
	gate := func(_ context.Context, host string) error {
		if host == "blocked.example" {
			return errors.New("redirect denied")
		}
		return nil
	}
	client := hardenedClient(gate, nil)
	via := make([]*http.Request, 1)
	req, err := http.NewRequest("GET", "http://blocked.example/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, via); err == nil ||
		!strings.Contains(err.Error(), "redirect denied") {
		t.Fatalf("CheckRedirect = %v, want denial", err)
	}
	if err := client.CheckRedirect(req, make([]*http.Request, maxRedirects)); err == nil {
		t.Fatal("CheckRedirect must stop at the redirect limit")
	}
}
