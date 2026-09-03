package search

import "testing"

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
