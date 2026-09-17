package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
)

type decodedEnvelope struct {
	Provider string   `json:"provider"`
	Query    string   `json:"query"`
	Results  []Result `json:"results"`
	Context  string   `json:"context"`
	Note     string   `json:"note"`
}

func execute(t *testing.T, tool *Tool, ctx context.Context, args string) decodedEnvelope {
	t.Helper()
	content, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	var out decodedEnvelope
	if err := json.Unmarshal([]byte(content.Text()), &out); err != nil {
		t.Fatalf("tool result is not valid JSON: %v\n%s", err, content.Text())
	}
	return out
}

// exaRateLimitNotice is the vendor's out-of-band free-tier message,
// delivered as an ordinary text payload on a 200 response.
const exaRateLimitNotice = "You've hit Exa's free MCP rate limit. " +
	"To continue using without limits, create your own Exa API key."

func TestExaRateLimitNoticeBecomesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "event: message\ndata: "+
				mcpTextEnvelope(t, exaRateLimitNotice)+"\n\n")
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:  ProviderExa,
		Endpoints: Endpoints{Exa: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), `{"query":"go"}`); err == nil {
		t.Fatal("a rate-limit notice must surface as an error")
	} else if !errdefs.IsRateLimit(err) {
		t.Fatalf("error = %v, want rate limit", err)
	}
}

func TestAutoFallsBackWhenFirstProviderIsRateLimited(t *testing.T) {
	limited := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "event: message\ndata: "+
				mcpTextEnvelope(t, exaRateLimitNotice)+"\n\n")
		}))
	defer limited.Close()
	healthy := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w,
				mcpTextEnvelope(t, readFixture(t, "parallel_search.json")))
		}))
	defer healthy.Close()
	tool, err := New(Settings{
		Provider: ProviderAuto,
		Endpoints: Endpoints{
			Exa:      limited.URL,
			Parallel: healthy.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pin the order so the rate-limited provider is tried first.
	tool.autoOrder = []string{ProviderExa, ProviderParallel}
	out := execute(t, tool, context.Background(), `{"query":"go"}`)
	if out.Provider != ProviderParallel || len(out.Results) == 0 {
		t.Fatalf("auto did not fall back: %+v", out)
	}
}

func TestToolDefinition(t *testing.T) {
	tool, err := New(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if def.Name != Name {
		t.Fatalf("name = %q", def.Name)
	}
	if !strings.Contains(strings.ToLower(def.Description), "search the web") {
		t.Fatalf("description must carry discovery keywords: %q", def.Description)
	}
	raw := def.InputSchema
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"query", "count", "freshness", "domains"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Fatalf("schema property %q missing: %s", want, raw)
		}
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Fatalf("required = %v", schema.Required)
	}
}

func TestToolArgsValidation(t *testing.T) {
	tool, err := New(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]string{
		"empty query":   `{"query":"  "}`,
		"bad freshness": `{"query":"go","freshness":"hour"}`,
		"too many domains": `{"query":"go","domains":` +
			`["a.com","b.com","c.com","d.com","e.com","f.com"]}`,
		"invalid domain": `{"query":"go","domains":["http://a.com"]}`,
		"syntax":         `{"query":`,
	} {
		if _, err := tool.Execute(context.Background(), args); err == nil {
			t.Errorf("%s: Execute accepted bad arguments", name)
		} else if !errdefs.IsValidation(err) {
			t.Errorf("%s: error = %v, want validation", name, err)
		}
	}
}

func TestToolCountClampsToConfiguredMax(t *testing.T) {
	tool, err := New(Settings{MaxResults: 3})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := tool.parseArgs(`{"query":"go","count":10}`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Count != 3 {
		t.Fatalf("count = %d, want clamp to 3", parsed.Count)
	}
	parsed, err = tool.parseArgs(`{"query":"go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Count != 3 {
		t.Fatalf("default count = %d, want 3", parsed.Count)
	}
}

func TestToolAutoSelectionIsStablePerConversation(t *testing.T) {
	tool, err := New(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.WithRunInfo(context.Background(),
		agent.RunInfo{Identity: agent.Identity{ConversationID: "conv-42"}})
	first := tool.pickProvider(ctx)
	for i := 0; i < 5; i++ {
		if got := tool.pickProvider(ctx); got != first {
			t.Fatalf("provider changed within one conversation: %q then %q",
				first, got)
		}
	}
}

func TestToolAutoFallsBackToTheOtherKeylessProvider(t *testing.T) {
	pdown := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusServiceUnavailable)
		}))
	defer pdown.Close()
	exa := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "event: message\ndata: "+
				mcpTextEnvelope(t, readFixture(t, "exa_search.txt"))+"\n\n")
		}))
	defer exa.Close()
	tool, err := New(Settings{
		Provider:  ProviderAuto,
		Endpoints: Endpoints{Parallel: pdown.URL, Exa: exa.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Whatever backend auto picks first, every call must succeed: a
	// failing first pick has to fall back to the other provider.
	for i := 0; i < 4; i++ {
		out := execute(t, tool, context.Background(),
			`{"query":"go http client"}`)
		if len(out.Results) == 0 {
			t.Fatalf("call %d returned no results: %+v", i, out)
		}
	}
}

func TestToolFallbackSkipsDeadlineErrors(t *testing.T) {
	tool, err := New(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tool.fallback(ProviderParallel,
		context.DeadlineExceeded); ok {
		t.Fatal("deadline errors must not trigger a cross-provider retry")
	}
	if _, ok := tool.fallback(ProviderParallel,
		context.Canceled); ok {
		t.Fatal("cancellation must not trigger a cross-provider retry")
	}
	next, ok := tool.fallback(ProviderParallel, errors.New("boom"))
	if !ok || next != ProviderExa {
		t.Fatalf("fallback = (%q, %v)", next, ok)
	}
}

func TestToolTavily(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			_, _ = io.WriteString(w, `{"results":[
				{"title":"Go docs","url":"https://go.dev/doc/",
				 "content":"The Go programming language documentation.",
				 "published_date":"2026-02-01"},
				{"title":"net/http","url":"https://pkg.go.dev/net/http",
				 "content":"Package http provides HTTP client.",
				 "published_date":""}]}`)
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:   ProviderTavily,
		MaxResults: 5,
		APIKeys:    APIKeys{Tavily: "tv-key"},
		Endpoints:  Endpoints{Tavily: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := execute(t, tool, context.Background(),
		`{"query":"go docs","freshness":"week","domains":["go.dev"]}`)
	if out.Provider != ProviderTavily || len(out.Results) != 2 {
		t.Fatalf("out = %+v", out)
	}
	if gotAuth != "Bearer tv-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotBody["query"] != "go docs" ||
		gotBody["max_results"] != float64(5) ||
		gotBody["time_range"] != "week" {
		t.Fatalf("body = %+v", gotBody)
	}
	domains, _ := gotBody["include_domains"].([]any)
	if len(domains) != 1 || domains[0] != "go.dev" {
		t.Fatalf("include_domains = %+v", gotBody["include_domains"])
	}
}

func TestToolBrave(t *testing.T) {
	var gotQuery, gotCount, gotFreshness, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			gotQuery = q.Get("q")
			gotCount = q.Get("count")
			gotFreshness = q.Get("freshness")
			gotToken = r.Header.Get("X-Subscription-Token")
			_, _ = io.WriteString(w, `{"web":{"results":[
				{"title":"Go","url":"https://go.dev/","description":"The Go site.",
				 "age":"2026-01-01"}]}}`)
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:   ProviderBrave,
		MaxResults: 4,
		APIKeys:    APIKeys{Brave: "bv-key"},
		Endpoints:  Endpoints{Brave: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := execute(t, tool, context.Background(),
		`{"query":"go","freshness":"year","domains":["go.dev","pkg.go.dev"]}`)
	if out.Provider != ProviderBrave || len(out.Results) != 1 {
		t.Fatalf("out = %+v", out)
	}
	if gotToken != "bv-key" {
		t.Fatalf("token = %q", gotToken)
	}
	if !strings.Contains(gotQuery, "(site:go.dev OR site:pkg.go.dev)") ||
		!strings.HasSuffix(gotQuery, " go") {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotCount != "4" || gotFreshness != "py" {
		t.Fatalf("count = %q freshness = %q", gotCount, gotFreshness)
	}
}

func TestToolExa(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("exaApiKey") != "ex-key" {
				t.Errorf("exa api key missing from endpoint: %s", r.URL)
			}
			_, _ = io.WriteString(w, "event: message\ndata: "+
				mcpTextEnvelope(t, readFixture(t, "exa_search.txt"))+"\n\n")
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:  ProviderExa,
		APIKeys:   APIKeys{Exa: "ex-key"},
		Endpoints: Endpoints{Exa: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := execute(t, tool, context.Background(), `{"query":"go"}`)
	if out.Provider != ProviderExa || len(out.Results) != 2 ||
		out.Context == "" {
		t.Fatalf("out = %+v", out)
	}
}

func TestToolParallel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"session_id":"conv-7"`) {
				t.Errorf("session id missing from request: %s", raw)
			}
			_, _ = io.WriteString(w,
				mcpTextEnvelope(t, readFixture(t, "parallel_search.json")))
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:  ProviderParallel,
		Endpoints: Endpoints{Parallel: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.WithRunInfo(context.Background(),
		agent.RunInfo{Identity: agent.Identity{ConversationID: "conv-7"}})
	out := execute(t, tool, ctx, `{"query":"go docs"}`)
	if out.Provider != ProviderParallel || len(out.Results) != 2 ||
		out.Context == "" {
		t.Fatalf("out = %+v", out)
	}
}

func TestToolEmptyResultsCarryNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, mcpTextEnvelope(t, `{"results":[]}`))
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:  ProviderParallel,
		Endpoints: Endpoints{Parallel: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := execute(t, tool, context.Background(), `{"query":"nothing"}`)
	if out.Note == "" {
		t.Fatalf("empty results must carry a note: %+v", out)
	}
	if out.Results == nil {
		t.Fatal("results must serialize as [] rather than null")
	}
}

func TestToolProviderHTTPErrorMapsToCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "slow down", http.StatusTooManyRequests)
		}))
	defer srv.Close()
	tool, err := New(Settings{
		Provider:  ProviderTavily,
		APIKeys:   APIKeys{Tavily: "tv"},
		Endpoints: Endpoints{Tavily: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(context.Background(), `{"query":"go"}`)
	if !errdefs.IsRateLimit(err) {
		t.Fatalf("err = %v, want rate limit", err)
	}
	if d, ok := errdefs.RetryAfter(err); !ok || d <= 0 {
		t.Fatalf("retry-after = %s (%v)", d, ok)
	}
}
