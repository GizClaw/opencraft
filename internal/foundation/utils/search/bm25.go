// Package search implements a tiny in-memory BM25 text index. It is
// deliberately dependency-free: the corpus is small (slash commands
// today), so a hand-rolled inverted index with field weighting beats
// pulling in a full search engine.
package search

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Doc is one indexed document. Name is the boosted field (command
// names); Text is the secondary field (descriptions).
type Doc struct {
	ID   string
	Name string
	Text string
}

// Result is one match from a Search, ranked by BM25 score.
type Result struct {
	ID    string
	Score float64
}

// Field weights: a term in the name field scores higher than the same
// term in the description field.
const (
	nameBoost = 3.0
	textBoost = 1.0
	// prefixWeight discounts a query term that only prefixes an
	// index term, so "/re" still surfaces "resume" but exact matches
	// outrank it.
	prefixWeight = 0.5
)

// BM25 parameters: k1 controls term-frequency saturation, b controls
// the length normalisation strength.
const (
	k1 = 1.2
	b  = 0.75
)

// fieldIndex holds the per-field statistics a BM25 score needs: each
// document's field length and the postings of every term.
//
// The postings are a flat, term-major slice instead of a
// map[term]map[doc]tf: a Go map costs ~200 bytes before it holds
// anything, so one map per distinct term dominated the heap for a
// corpus of a few hundred documents (476 skills, ~2.7k distinct
// terms, ~0.9MB). Terms are kept sorted, so a query term resolves with
// one binary search and its prefix matches with a scan from there.
type fieldIndex struct {
	lens     []int
	totalLen int
	terms    []termEntry
	postings []posting
	// pending collects postings while documents are added; finalize
	// folds them into terms/postings and drops the slice.
	pending []pendingPosting
}

// termEntry is one distinct term and the slice of postings it owns.
type termEntry struct {
	term string
	off  int32
	df   int32
}

// posting is one document's term frequency for the owning term.
type posting struct {
	doc int32
	tf  int32
}

// pendingPosting is a collected posting before finalize groups it under
// its term.
type pendingPosting struct {
	term string
	doc  int32
	tf   int32
}

// Index is a read-only BM25 index over a fixed document set.
type Index struct {
	docs   []Doc
	fields [2]fieldIndex // 0 = name, 1 = text
	nDocs  int
}

// NewIndex builds an index over docs. Field statistics are computed
// once; the index is immutable afterwards.
func NewIndex(docs []Doc) *Index {
	ix := &Index{docs: docs, nDocs: len(docs)}
	for d, doc := range docs {
		ix.fields[0].collect(int32(d), doc.Name)
		ix.fields[1].collect(int32(d), doc.Text)
	}
	for i := range ix.fields {
		ix.fields[i].finalize()
	}
	return ix
}

// collect appends one document's postings for a field. Terms are
// deduplicated by sorting the token slice in place, so adding a
// document allocates no per-document map.
//
// Field length is the distinct-token count, not the total token count,
// and each term is posted at most once per field. tf is therefore
// almost always 1 and the k1 saturation term in Search stays inert.
// That is deliberate: with a handful of short documents, ranking is
// dominated by the name/text field boost and IDF, and full BM25
// term-frequency or length normalisation would only add noise.
func (f *fieldIndex) collect(doc int32, text string) {
	tokens := Tokenize(text)
	sort.Strings(tokens)
	distinct := 0
	for i := 0; i < len(tokens); {
		j := i + 1
		for j < len(tokens) && tokens[j] == tokens[i] {
			j++
		}
		f.pending = append(f.pending, pendingPosting{
			term: tokens[i], doc: doc, tf: int32(j - i),
		})
		distinct++
		i = j
	}
	f.lens = append(f.lens, distinct)
	f.totalLen += distinct
}

// finalize sorts the collected postings by term and folds them into the
// flat term table. A term's postings stay in document order, which is
// what lets docFreq merge two fields without a set.
func (f *fieldIndex) finalize() {
	pending := f.pending
	f.pending = nil
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].term != pending[j].term {
			return pending[i].term < pending[j].term
		}
		return pending[i].doc < pending[j].doc
	})
	f.postings = make([]posting, 0, len(pending))
	f.terms = make([]termEntry, 0, len(pending))
	for i, p := range pending {
		if i == 0 || pending[i-1].term != p.term {
			f.terms = append(f.terms, termEntry{
				term: p.term,
				off:  int32(len(f.postings)),
			})
		}
		f.postings = append(f.postings, posting{doc: p.doc, tf: p.tf})
		f.terms[len(f.terms)-1].df++
	}
}

// lookup returns the entry of an exact term.
func (f *fieldIndex) lookup(term string) (termEntry, bool) {
	i, ok := f.search(term)
	if !ok {
		return termEntry{}, false
	}
	return f.terms[i], true
}

// search returns the position of the first term >= target and whether
// that term is an exact match.
func (f *fieldIndex) search(target string) (int, bool) {
	i := sort.Search(len(f.terms), func(i int) bool {
		return f.terms[i].term >= target
	})
	return i, i < len(f.terms) && f.terms[i].term == target
}

// postingsOf returns the postings of one term, in document order.
func (f *fieldIndex) postingsOf(term string) []posting {
	e, ok := f.lookup(term)
	if !ok {
		return nil
	}
	return f.postings[e.off : e.off+e.df]
}

// eachPrefix calls fn for every indexed term that has prefix as a
// proper prefix. Terms are sorted, so the scan starts at the first term
// >= prefix and stops at the first term that no longer matches.
func (f *fieldIndex) eachPrefix(prefix string, fn func(termEntry)) {
	i, _ := f.search(prefix)
	for ; i < len(f.terms); i++ {
		term := f.terms[i].term
		if !strings.HasPrefix(term, prefix) {
			return
		}
		if len(term) > len(prefix) {
			fn(f.terms[i])
		}
	}
}

// Search ranks documents against the query with BM25 over both the
// name and text fields. Query terms also match by prefix, so a
// fragment like "re" still surfaces "resume". An empty query returns
// every document (score 0) in registration order.
func (ix *Index) Search(query string, limit int) []Result {
	terms := uniqueTokens(query)
	if len(terms) == 0 {
		n := min(limit, len(ix.docs))
		res := make([]Result, 0, n)
		for i := 0; i < n; i++ {
			res = append(res, Result{ID: ix.docs[i].ID})
		}
		return res
	}

	// Expand each query term to the index terms it hits: the exact
	// term plus every term it prefixes. Prefix hits score lower.
	type termMatch struct {
		term   string
		df     int
		prefix bool
	}
	matches := make([]termMatch, 0, len(terms)*2)
	seen := make(map[string]bool, len(terms))
	// Document frequency is resolved once per matched term; df counts
	// documents, so a document that contains the term in both its name
	// and its text counts once.
	add := func(term string, prefix bool) {
		if seen[term] {
			return
		}
		seen[term] = true
		matches = append(matches, termMatch{
			term: term, df: ix.docFreq(term), prefix: prefix,
		})
	}
	for _, qt := range terms {
		add(qt, false)
		for field := range ix.fields {
			ix.fields[field].eachPrefix(qt, func(entry termEntry) {
				add(entry.term, true)
			})
		}
	}

	// One score per document, accumulated term by term, so the work is
	// proportional to the postings a term actually has rather than to
	// documents × matched terms.
	scores := make([]float64, ix.nDocs)
	for _, tm := range matches {
		idf := math.Log(1 +
			(float64(ix.nDocs)-float64(tm.df)+0.5)/(float64(tm.df)+0.5))
		for field, base := range [2]float64{nameBoost, textBoost} {
			f := &ix.fields[field]
			if f.totalLen == 0 {
				continue
			}
			boost := base
			if tm.prefix {
				boost *= prefixWeight
			}
			avg := float64(f.totalLen) / float64(ix.nDocs)
			for _, p := range f.postingsOf(tm.term) {
				tf := float64(p.tf)
				denom := tf + k1*(1-b+b*float64(f.lens[p.doc])/avg)
				scores[p.doc] += boost * idf * tf * (k1 + 1) / denom
			}
		}
	}

	scored := make([]Result, 0, len(ix.docs))
	for d, score := range scores {
		if score > 0 {
			scored = append(scored, Result{ID: ix.docs[d].ID, Score: score})
		}
	}
	// Stable so equal scores keep registration order.
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// docFreq returns how many documents contain term in either field. A
// document that hits in both its name and its text counts once, so
// this is the union across fields rather than the sum.
func (ix *Index) docFreq(term string) int {
	name := ix.fields[0].postingsOf(term)
	text := ix.fields[1].postingsOf(term)
	switch {
	case len(name) == 0:
		return len(text)
	case len(text) == 0:
		return len(name)
	}
	// Both lists are in document order: merge and count distinct docs.
	df, i, j := 0, 0, 0
	for i < len(name) && j < len(text) {
		switch {
		case name[i].doc == text[j].doc:
			i, j = i+1, j+1
		case name[i].doc < text[j].doc:
			i++
		default:
			j++
		}
		df++
	}
	return df + (len(name) - i) + (len(text) - j)
}

// uniqueTokens tokenizes the query and keeps each term once, so a
// repeated word cannot double-count its IDF contribution.
func uniqueTokens(query string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range Tokenize(query) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Tokenize splits text into search terms: ASCII words, CJK characters
// and CJK bigrams. Bigrams let Chinese descriptions match query
// fragments without a segmentation library.
func Tokenize(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	var word strings.Builder
	var prevCJK rune
	flushWord := func() {
		if word.Len() > 0 {
			tokens = append(tokens, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		switch {
		case isCJK(r):
			flushWord()
			if prevCJK != 0 {
				tokens = append(tokens, string(prevCJK)+string(r))
			}
			tokens = append(tokens, string(r))
			prevCJK = r
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
			prevCJK = 0
		default:
			flushWord()
			prevCJK = 0
		}
	}
	flushWord()
	return tokens
}

// isCJK covers the scripts that have no whitespace word boundaries:
// Han, Hiragana, Katakana and Hangul.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
