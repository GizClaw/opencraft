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
// document's field length and the term -> doc -> frequency postings.
type fieldIndex struct {
	lens     []int
	totalLen int
	postings map[string]map[int]int
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
	ix := &Index{
		docs: docs,
		fields: [2]fieldIndex{
			{postings: make(map[string]map[int]int)},
			{postings: make(map[string]map[int]int)},
		},
		nDocs: len(docs),
	}
	for d, doc := range docs {
		ix.add(d, 0, doc.Name)
		ix.add(d, 1, doc.Text)
	}
	return ix
}

func (ix *Index) add(doc int, field int, text string) {
	f := &ix.fields[field]
	tfs := make(map[string]int)
	for _, term := range Tokenize(text) {
		tfs[term]++
	}
	// Field length is the distinct-token count, not the total token
	// count, and each term is posted at most once per field. tf is
	// therefore almost always 1 and the k1 saturation term in Search
	// stays inert. That is deliberate: with a handful of short
	// commands, ranking is dominated by the name/text field boost and
	// IDF, and full BM25 term-frequency or length normalisation would
	// only add noise.
	f.lens = append(f.lens, len(tfs))
	f.totalLen += len(tfs)
	for term, tf := range tfs {
		if f.postings[term] == nil {
			f.postings[term] = make(map[int]int)
		}
		f.postings[term][doc] = tf
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
		prefix bool
	}
	matches := make([]termMatch, 0, len(terms)*2)
	seen := make(map[string]bool, len(terms))
	for _, qt := range terms {
		if !seen[qt] {
			seen[qt] = true
			matches = append(matches, termMatch{term: qt})
		}
		for field := range ix.fields {
			for it := range ix.fields[field].postings {
				if len(it) > len(qt) && strings.HasPrefix(it, qt) &&
					!seen[it] {
					seen[it] = true
					matches = append(matches, termMatch{term: it, prefix: true})
				}
			}
		}
	}

	// Document frequency per matched term across both fields, then
	// IDF. df counts documents, so a document that contains the term
	// in both its name and its text is counted once — a per-field
	// length sum would over-count it and depress the IDF.
	idf := make(map[string]float64, len(matches))
	for _, tm := range matches {
		df := ix.docFreq(tm.term)
		idf[tm.term] = math.Log(1 +
			(float64(ix.nDocs)-float64(df)+0.5)/(float64(df)+0.5))
	}

	scored := make([]Result, 0, len(ix.docs))
	for d := range ix.docs {
		var score float64
		for _, tm := range matches {
			for field, boost := range []float64{nameBoost, textBoost} {
				f := &ix.fields[field]
				tf, ok := f.postings[tm.term][d]
				if !ok || f.totalLen == 0 {
					continue
				}
				if tm.prefix {
					boost *= prefixWeight
				}
				avg := float64(f.totalLen) / float64(ix.nDocs)
				denom := float64(tf) +
					k1*(1-b+b*float64(f.lens[d])/avg)
				score += boost * idf[tm.term] *
					float64(tf) * (k1 + 1) / denom
			}
		}
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
	df := 0
	for d := range ix.docs {
		if _, ok := ix.fields[0].postings[term][d]; ok {
			df++
		} else if _, ok := ix.fields[1].postings[term][d]; ok {
			df++
		}
	}
	return df
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
