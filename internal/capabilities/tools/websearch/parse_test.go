package websearch

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseExaText(t *testing.T) {
	resp := parseExaText(readFixture(t, "exa_search.txt"), 8)
	if len(resp.Results) != 2 {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[0].URL != "https://pkg.go.dev/net/http" {
		t.Fatalf("first url = %q", resp.Results[0].URL)
	}
	if resp.Results[0].Published != "" {
		t.Fatalf("N/A must normalize to empty, got %q", resp.Results[0].Published)
	}
	if resp.Results[1].Published != "2026-03-02" {
		t.Fatalf("published = %q", resp.Results[1].Published)
	}
	if resp.Context == "" {
		t.Fatal("exa text must be returned as context")
	}
}

func TestParseExaTextRespectsCount(t *testing.T) {
	resp := parseExaText(readFixture(t, "exa_search.txt"), 1)
	if len(resp.Results) != 1 {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestParseExaTextSkipsBlocksWithoutURL(t *testing.T) {
	resp := parseExaText("Title: broken\nPublished: N/A\nHighlights:\nnothing", 8)
	if len(resp.Results) != 0 {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestParseParallelText(t *testing.T) {
	resp, err := parseParallelText(readFixture(t, "parallel_search.json"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[1].Published != "2026-01-15" {
		t.Fatalf("published = %q", resp.Results[1].Published)
	}
	if resp.Context == "" {
		t.Fatal("excerpts must be returned as context")
	}
}

func TestParseParallelTextRejectsBadPayload(t *testing.T) {
	if _, err := parseParallelText("not json", 8); err == nil {
		t.Fatal("bad payload must fail")
	}
}

func TestParseParallelTextRejectsMissingResults(t *testing.T) {
	if _, err := parseParallelText(`{"error":"quota exceeded"}`, 8); err == nil {
		t.Fatal("a payload without a results field must fail, not read as empty")
	}
}
