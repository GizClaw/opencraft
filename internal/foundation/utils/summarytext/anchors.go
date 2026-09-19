package summarytext

import (
	"regexp"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
)

// The anchor index: identifiers the model needs verbatim that a
// summarizer is entitled to paraphrase away. A condensed summary is
// prose — the model that wrote it may drop a path, a commit SHA or a PR
// number that a later turn still needs, and the field evidence is
// unambiguous that this loss is what makes folded context unusable
// (paths and refs are the first things a reader needs to act). So the
// identifiers are extracted mechanically from the folded messages and
// appended to the summary as-is: nothing here is generated, ranked by an
// LLM, or rewritten.
const (
	// anchorMaxItems caps one list so a pathological message (a huge
	// directory listing) cannot turn the index into a second summary.
	anchorMaxItems = 40
	// anchorMaxChars caps the whole rendered index.
	anchorMaxChars = 2000
	// anchorMaxPathLen drops absurd "paths" (minified bundles, data
	// URLs) that a loose pattern can match.
	anchorMaxPathLen = 200
)

var (
	// anchorPathRe matches repo-relative and absolute file paths. It
	// requires a separator and a short extension so prose words are not
	// collected as identifiers.
	anchorPathRe = regexp.MustCompile(
		`(?:[A-Za-z0-9_.@+-]+/)+[A-Za-z0-9_.@+-]+\.[A-Za-z0-9]{1,8}`)
	// anchorSHARe matches abbreviated and full git object names.
	anchorSHARe = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// anchorRefRe matches "#123" style issue / PR references.
	anchorRefRe = regexp.MustCompile(`#\d{1,7}\b`)
	// anchorKeyRe matches ticket keys ("ABC-123").
	anchorKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-\d{1,5}\b`)
	// anchorKeyDeny drops standard designators that share the ticket-key
	// shape: "UTF-8", "ISO-8859-1", "SHA-256" are not work items, and a
	// list of them is noise a reader has to skip.
	anchorKeyDeny = map[string]bool{
		"UTF": true, "ISO": true, "SHA": true, "AES": true, "DES": true,
		"RSA": true, "HTTP": true, "TLS": true, "SSL": true, "CP": true,
		"GB": true, "ECMA": true, "IPV": true, "ES": true,
	}
)

// AnchorIndex is the mechanical identifier extraction of a message set.
type AnchorIndex struct {
	Paths []string
	SHAs  []string
	Refs  []string
	Keys  []string
	// seen deduplicates across lists and AddText calls. It is not part of
	// the rendered form.
	seen map[string]bool
}

func (a *AnchorIndex) init() {
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
}

func (a *AnchorIndex) add(dst *[]string, value string) {
	a.init()
	if len(*dst) >= anchorMaxItems || a.seen[value] {
		return
	}
	a.seen[value] = true
	*dst = append(*dst, value)
}

// AddText merges the identifiers found in already-rendered text into the
// index. Callers use it for a previous summary: its verbatim block carries
// identifiers from earlier folds that this fold's message set no longer
// contains, and dropping them would lose exactly the identifiers the
// index exists to preserve.
func (a *AnchorIndex) AddText(text string) {
	if text == "" {
		return
	}
	for _, path := range anchorPathRe.FindAllString(text, -1) {
		if len(path) > anchorMaxPathLen {
			continue
		}
		a.add(&a.Paths, path)
	}
	for _, sha := range anchorSHARe.FindAllString(text, -1) {
		a.add(&a.SHAs, sha)
	}
	for _, ref := range anchorRefRe.FindAllString(text, -1) {
		a.add(&a.Refs, ref)
	}
	for _, key := range anchorKeyRe.FindAllString(text, -1) {
		if prefix, _, ok := strings.Cut(key, "-"); ok && anchorKeyDeny[prefix] {
			continue
		}
		a.add(&a.Keys, key)
	}
}

// Empty reports whether nothing was found.
func (a AnchorIndex) Empty() bool {
	return len(a.Paths) == 0 && len(a.SHAs) == 0 &&
		len(a.Refs) == 0 && len(a.Keys) == 0
}

// ExtractAnchors scans rendered messages for identifiers, keeping
// first-seen order within each list (the order the conversation
// introduced them) and dropping duplicates.
func ExtractAnchors(msgs []message.Message) AnchorIndex {
	var idx AnchorIndex
	for _, m := range msgs {
		text := RenderMessage(m)
		if text == "" {
			continue
		}
		idx.AddText(text)
	}
	return idx
}

// Render renders the index as a markdown block appended to a compaction
// summary, or "" when there is nothing to add. The block is bounded: each
// list is capped by extraction, and the whole block is truncated at
// anchorMaxChars.
func (a AnchorIndex) Render() string {
	if a.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Identifiers (extracted verbatim from the folded messages)\n")
	writeList := func(label string, values []string) {
		if len(values) == 0 {
			return
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(strings.Join(values, ", "))
		b.WriteString("\n")
	}
	writeList("Files", a.Paths)
	writeList("Commits", a.SHAs)
	writeList("Issues/PRs", a.Refs)
	writeList("Tickets", a.Keys)
	out := strings.TrimRight(b.String(), "\n")
	// Runes, not bytes: a path with a non-ASCII character must not be cut in
	// half (the block is appended to a summary that a provider re-tokenizes).
	if runes := []rune(out); len(runes) > anchorMaxChars {
		out = strings.TrimRight(string(runes[:anchorMaxChars]), " \n")
	}
	return out
}
