package sessionsearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// fakeSearcher records the search call and returns a canned result.
type fakeSearcher struct {
	query  string
	opts   sessions.SearchOptions
	result sessions.SearchResult
	err    error
}

func (f *fakeSearcher) SearchMessages(
	_ context.Context, query string, opts sessions.SearchOptions,
) (sessions.SearchResult, error) {
	f.query = query
	f.opts = opts
	return f.result, f.err
}

// runTool executes the tool and decodes the JSON envelope.
func runTool(t *testing.T, searcher Searcher, args string) envelope {
	t.Helper()
	content, err := New(searcher).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out envelope
	if err := json.Unmarshal([]byte(content.Text()), &out); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return out
}

func TestToolDefinition(t *testing.T) {
	def := New(&fakeSearcher{}).Definition()
	if def.Name != Name {
		t.Fatalf("name = %q", def.Name)
	}
	// Discovery is BM25 over name and description: a recall request
	// must be phrased with the same words the model saw in the prompt.
	lower := strings.ToLower(def.Description)
	for _, want := range []string{"past conversations", "earlier sessions", "decided", "web_search"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("description is missing %q: %q", want, def.Description)
		}
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["query"]; !ok {
		t.Fatalf("schema has no query property: %s", def.InputSchema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Fatalf("required = %v", schema.Required)
	}
}

func TestExecuteCollapsesAndShapesHits(t *testing.T) {
	searcher := &fakeSearcher{result: sessions.SearchResult{
		Hits: []sessions.SearchHit{{
			ConversationID: "s-1",
			Title:          "检索方案",
			RunID:          "run-1",
			Role:           "user",
			At:             time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
			TurnSeq:        1,
			Seq:            2,
			Snippet:        "我们决定采用 [跨会话] 检索",
		}},
	}}
	out := runTool(t, searcher, `{"query":"跨会话"}`)
	if searcher.query != "跨会话" {
		t.Fatalf("query = %q", searcher.query)
	}
	if !searcher.opts.Collapse || searcher.opts.Limit != defaultLimit {
		t.Fatalf("opts = %+v, want collapsed at the default limit", searcher.opts)
	}
	if out.Query != "跨会话" || len(out.Hits) != 1 {
		t.Fatalf("envelope = %+v", out)
	}
	got := out.Hits[0]
	if got.ConversationID != "s-1" || got.Ref != "s-1#2" ||
		got.At != "2026-09-21T10:00:00Z" || got.RunID != "run-1" {
		t.Fatalf("hit = %+v", got)
	}
	if !strings.Contains(got.Snippet, "[跨会话]") {
		t.Fatalf("snippet = %q", got.Snippet)
	}
	if out.Note != "" || out.Truncated {
		t.Fatalf("envelope = %+v, want no note and no truncation", out)
	}
}

func TestExecuteClampsLimit(t *testing.T) {
	for _, tc := range []struct {
		args string
		want int
	}{
		{`{"query":"go"}`, defaultLimit},
		{`{"query":"go","limit":0}`, defaultLimit},
		{`{"query":"go","limit":3}`, 3},
		{`{"query":"go","limit":99}`, maxLimit},
	} {
		searcher := &fakeSearcher{}
		runTool(t, searcher, tc.args)
		if searcher.opts.Limit != tc.want {
			t.Errorf("%s: limit = %d, want %d", tc.args, searcher.opts.Limit, tc.want)
		}
	}
}

func TestExecuteNotes(t *testing.T) {
	empty := runTool(t, &fakeSearcher{}, `{"query":"nothing"}`)
	if len(empty.Hits) != 0 || empty.Note != noResultsNote {
		t.Fatalf("empty envelope = %+v", empty)
	}
	substring := runTool(t, &fakeSearcher{result: sessions.SearchResult{
		Hits:      []sessions.SearchHit{{ConversationID: "s-1", Role: "user"}},
		Substring: true,
	}}, `{"query":"AI"}`)
	if substring.Note != shortQueryNote {
		t.Fatalf("substring note = %q", substring.Note)
	}
	truncated := runTool(t, &fakeSearcher{result: sessions.SearchResult{
		Hits:      []sessions.SearchHit{{ConversationID: "s-1", Role: "user"}},
		Truncated: true,
	}}, `{"query":"deploy"}`)
	if !truncated.Truncated {
		t.Fatalf("truncated envelope = %+v", truncated)
	}
}

func TestExecuteValidatesArguments(t *testing.T) {
	tool := New(&fakeSearcher{})
	for name, args := range map[string]string{
		"empty query": `{"query":"  "}`,
		"syntax":      `{"query":`,
	} {
		if _, err := tool.Execute(context.Background(), args); err == nil {
			t.Errorf("%s: Execute accepted bad arguments", name)
		} else if !errdefs.IsValidation(err) {
			t.Errorf("%s: error = %v, want a validation error", name, err)
		}
	}
	// A store failure surfaces unchanged: the middleware classifies it.
	boom := errors.New("boom")
	if _, err := New(&fakeSearcher{err: boom}).Execute(
		context.Background(), `{"query":"go"}`); !errors.Is(err, boom) {
		t.Errorf("store error = %v, want the original error", err)
	}
}

func TestExecuteResultIsText(t *testing.T) {
	content, err := New(&fakeSearcher{}).Execute(context.Background(), `{"query":"go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(content.Parts) != 1 {
		t.Fatalf("parts = %+v", content.Parts)
	}
	if _, ok := content.Parts[0].(message.TextPart); !ok {
		t.Fatalf("part = %T, want text", content.Parts[0])
	}
}
