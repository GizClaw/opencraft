package search

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"resume", []string{"resume"}},
		{"switch sandbox mode", []string{"switch", "sandbox", "mode"}},
		{"clean-up", []string{"clean", "up"}},
		{"清理会话", []string{"清", "清理", "理", "理会", "会", "会话", "话"}},
		{"", nil},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		if len(got) != len(c.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Tokenize(%q) = %v, want %v",
					c.in, got, c.want)
				break
			}
		}
	}
}

func TestSearchMatchesNameAndDescription(t *testing.T) {
	ix := NewIndex([]Doc{
		{ID: "resume", Name: "resume",
			Text: "pick and resume a past conversation"},
		{ID: "permissions", Name: "permissions",
			Text: "switch the sandbox permission mode"},
		{ID: "clear", Name: "clear",
			Text: "清理当前会话历史"},
	})

	// Name match ranks above description-only matches.
	res := ix.Search("permissions", 10)
	if len(res) == 0 || res[0].ID != "permissions" {
		t.Fatalf("Search(permissions) = %+v, want permissions first", res)
	}

	// Description match.
	res = ix.Search("sandbox", 10)
	if len(res) == 0 || res[0].ID != "permissions" {
		t.Fatalf("Search(sandbox) = %+v, want permissions", res)
	}

	// CJK description match.
	res = ix.Search("清理", 10)
	if len(res) == 0 || res[0].ID != "clear" {
		t.Fatalf("Search(清理) = %+v, want clear", res)
	}

	// No match -> empty.
	if res := ix.Search("zzzz", 10); len(res) != 0 {
		t.Errorf("Search(zzzz) = %+v, want none", res)
	}
}

func TestSearchMatchesPrefixes(t *testing.T) {
	ix := NewIndex([]Doc{
		{ID: "resume", Name: "resume",
			Text: "pick and resume a past conversation"},
		{ID: "permissions", Name: "permissions",
			Text: "switch the sandbox permission mode"},
		{ID: "clear", Name: "clear",
			Text: "清理当前会话历史"},
	})

	// A fragment ranks the command whose name starts with it first.
	res := ix.Search("re", 10)
	if len(res) == 0 || res[0].ID != "resume" {
		t.Fatalf("Search(re) = %+v, want resume first", res)
	}
	res = ix.Search("per", 10)
	if len(res) == 0 || res[0].ID != "permissions" {
		t.Fatalf("Search(per) = %+v, want permissions first", res)
	}
	// Prefix of a description word matches too.
	res = ix.Search("conversa", 10)
	if len(res) == 0 || res[0].ID != "resume" {
		t.Fatalf("Search(conversa) = %+v, want resume", res)
	}
}

func TestSearchEmptyQueryReturnsAll(t *testing.T) {
	ix := NewIndex([]Doc{
		{ID: "resume", Name: "resume"},
		{ID: "permissions", Name: "permissions"},
	})
	res := ix.Search("", 10)
	if len(res) != 2 {
		t.Fatalf("Search(empty) = %+v, want both docs", res)
	}
	if res[0].ID != "resume" || res[1].ID != "permissions" {
		t.Errorf("empty query must keep registration order: %+v", res)
	}
}

func TestSearchLimit(t *testing.T) {
	ix := NewIndex([]Doc{
		{ID: "resume", Name: "resume"},
		{ID: "permissions", Name: "permissions"},
		{ID: "clear", Name: "clear"},
	})
	if res := ix.Search("", 2); len(res) != 2 {
		t.Errorf("limit = %+v, want 2 results", res)
	}
	if res := ix.Search("resume", 1); len(res) != 1 || res[0].ID != "resume" {
		t.Errorf("limited search = %+v, want resume", res)
	}
}

func TestDocFreqCountsDocOnceAcrossFields(t *testing.T) {
	ix := NewIndex([]Doc{
		{ID: "a", Name: "alpha", Text: "alpha beta"},
		{ID: "b", Name: "beta", Text: "gamma"},
	})
	// "alpha" appears in a's name and a's text: still one document.
	// Summing the per-field postings would count it twice.
	if got := ix.docFreq("alpha"); got != 1 {
		t.Errorf("docFreq(alpha) = %d, want 1 (union across fields)", got)
	}
	// "beta" appears in a's text and b's name: two documents.
	if got := ix.docFreq("beta"); got != 2 {
		t.Errorf("docFreq(beta) = %d, want 2", got)
	}
}

// TestIndexStaysFlat guards the postings representation. One Go map per
// distinct term — the shape this replaced — costs ~200 bytes and one
// live object each: this corpus (500 documents, ~900 distinct words,
// ~15k postings) held 5,611 objects with per-term maps and holds ~1,000
// with the flat term table, and the real 476-skill corpus went from
// 0.93MB / 9,755 objects to 0.41MB / 2,859. The bound below fails
// loudly if per-term maps come back.
func TestIndexStaysFlat(t *testing.T) {
	docs := make([]Doc, 0, 500)
	for i := 0; i < 500; i++ {
		var text strings.Builder
		for j := 0; j < 30; j++ {
			fmt.Fprintf(&text, "w%d ", (i*7+j*13)%900)
		}
		id := fmt.Sprintf("skill-%d", i)
		docs = append(docs, Doc{ID: id, Name: id, Text: text.String()})
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	ix := NewIndex(docs)
	runtime.GC()
	runtime.ReadMemStats(&after)

	objects := int64(after.HeapObjects) - int64(before.HeapObjects)
	if objects > 3000 {
		t.Errorf("index holds %d live objects, want <= 3000: "+
			"the postings went back to one map per term", objects)
	}
	if res := ix.Search("w42", 5); len(res) == 0 {
		t.Errorf("Search(w42) = %v, want hits", res)
	}
	runtime.KeepAlive(ix)
}
