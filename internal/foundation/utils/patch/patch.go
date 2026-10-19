// Package patch implements the codex-style apply_patch format: a
// minimal line-based patch engine (parse, workspace apply, host
// directory apply, diff rendering) shared by the apply_patch tool and
// skill_modify's partial editing.
package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/workspace"
)

type opKind int

const (
	opAdd opKind = iota
	opUpdate
	opDelete
)

type op struct {
	kind  opKind
	path  string
	body  []string // add: content lines
	hunks []hunk   // update
}

type hunk struct {
	anchor  string
	removed []string
	added   []string
	// atLine is the position a hunk with no context and no anchor
	// applies to: a pure-insertion hunk in a unified diff
	// ("@@ -5,0 +6,2 @@") means "insert after old line 5". Zero means
	// unset.
	atLine int
	// entries keeps the hunk's lines in their original order for
	// rendering. removed/added stay the apply-side projection.
	entries []hunkEntry
	// unified marks a hunk parsed from a standard unified diff, whose
	// header carries the insertion position.
	unified bool
}

// hunkEntry is one hunk line in file order.
type hunkEntry struct {
	kind DiffLineKind
	text string
}

// ParseAny parses either the codex envelope or a standard unified diff.
// The envelope is what the model is asked to write; accepting unified
// diffs as well lets a patch produced by `git diff` be applied with the
// same tool and the same safety checks.
func ParseAny(patch string) ([]*op, error) {
	if LooksLikeUnifiedDiff(patch) {
		return parseUnifiedDiffOps(patch)
	}
	return Parse(patch)
}

// Parse parses the codex apply_patch format:
//
//	*** Begin Patch
//	*** Add File: path
//	+line
//	*** Update File: path
//	@@ context
//	-old
//	+new
//	*** Delete File: path
//	*** End Patch
func Parse(patch string) ([]*op, error) {
	lines := strings.Split(patch, "\n")
	var ops []*op
	var current *op
	inPatch := false

	for i := range lines {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "*** Begin Patch":
			if inPatch {
				return nil, errdefs.Validationf("apply_patch: duplicate Begin Patch")
			}
			inPatch = true
		case trimmed == "*** End Patch":
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: End Patch without Begin")
			}
			if current != nil && current.kind == opUpdate && len(current.hunks) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: update %q has no hunks", current.path)
			}
			return ops, nil
		case strings.HasPrefix(trimmed, "*** Add File: "):
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: Add File outside patch")
			}
			if current != nil && current.kind == opAdd && len(current.body) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: add %q has no lines", current.path)
			}
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File: "))
			if err := validatePath(p); err != nil {
				return nil, err
			}
			current = &op{kind: opAdd, path: p}
			ops = append(ops, current)
		case strings.HasPrefix(trimmed, "*** Update File: "):
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: Update File outside patch")
			}
			if current != nil && current.kind == opUpdate && len(current.hunks) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: update %q has no hunks", current.path)
			}
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File: "))
			if err := validatePath(p); err != nil {
				return nil, err
			}
			current = &op{kind: opUpdate, path: p}
			ops = append(ops, current)
		case strings.HasPrefix(trimmed, "*** Delete File: "):
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: Delete File outside patch")
			}
			if current != nil && current.kind == opAdd && len(current.body) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: add %q has no lines", current.path)
			}
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Delete File: "))
			if err := validatePath(p); err != nil {
				return nil, err
			}
			current = &op{kind: opDelete, path: p}
			ops = append(ops, current)
		case !inPatch:
			return nil, errdefs.Validationf(
				"apply_patch: unexpected line outside patch: %q", line)
		default:
			if current == nil {
				return nil, errdefs.Validationf(
					"apply_patch: unexpected line before any file op: %q", line)
			}
			if current.kind == opAdd {
				if !strings.HasPrefix(line, "+") {
					return nil, errdefs.Validationf(
						"apply_patch: add line for %q must start with '+': %q",
						current.path, line)
				}
				current.body = append(current.body, strings.TrimPrefix(line, "+"))
				continue
			}
			if current.kind == opUpdate {
				switch {
				case strings.HasPrefix(line, "@@"):
					current.hunks = append(current.hunks, hunk{
						anchor: strings.TrimSpace(strings.TrimPrefix(line, "@@")),
					})
				case len(current.hunks) == 0:
					return nil, errdefs.Validationf(
						"apply_patch: update %q line before first @@: %q",
						current.path, line)
				case strings.HasPrefix(line, "-"):
					h := &current.hunks[len(current.hunks)-1]
					h.removed = append(h.removed, strings.TrimPrefix(line, "-"))
				case strings.HasPrefix(line, "+"):
					h := &current.hunks[len(current.hunks)-1]
					h.added = append(h.added, strings.TrimPrefix(line, "+"))
				case strings.HasPrefix(line, " "):
					h := &current.hunks[len(current.hunks)-1]
					content := strings.TrimPrefix(line, " ")
					h.removed = append(h.removed, content)
					h.added = append(h.added, content)
				case line == "":
					// trailing blank inside a hunk: ignore
				case strings.HasPrefix(trimmed, "***"):
					return nil, errdefs.Validationf(
						"apply_patch: update %q unknown directive: %q",
						current.path, trimmed)
				default:
					// A context line whose own content starts with a tab
					// (or any character but a space): the leading space
					// marker is optional in the apply_patch format, and
					// models routinely write the line verbatim. Matching
					// still happens against the real file, so a line that
					// is not actually there fails the hunk instead of
					// misapplying it.
					h := &current.hunks[len(current.hunks)-1]
					h.removed = append(h.removed, line)
					h.added = append(h.added, line)
				}
				continue
			}
			// delete op: ignore stray blank lines
			if line != "" {
				return nil, errdefs.Validationf(
					"apply_patch: delete %q must not have a body: %q",
					current.path, line)
			}
		}
	}
	return nil, errdefs.Validationf("apply_patch: missing End Patch marker")
}

// validatePath rejects paths that do not name a file inside the target.
//
// Paths are judged in the workspace namespace, which is slash-separated
// on every platform: the codex envelope and `git diff` are both written
// that way, and the host applier converts with filepath.FromSlash.
// Backslashes are folded to slashes before judging, because Windows also
// reads them as separators: filepath.Clean turns "../x" into "..\x"
// there, so a check spelled against the host separator misses on the one
// platform it exists to protect. Folding also refuses "..\x" on hosts
// where a backslash is an ordinary character, which keeps one patch from
// naming two different files on two platforms.
//
// Both readings of "absolute" are refused: a leading slash (path.IsAbs)
// is absolute on every platform, while Windows drive and UNC forms are
// only absolute to filepath.IsAbs.
func validatePath(p string) error {
	if p == "" {
		return errdefs.Validationf("apply_patch: empty file path")
	}
	slashed := strings.ReplaceAll(p, `\`, "/")
	if path.IsAbs(slashed) || filepath.IsAbs(p) {
		return errdefs.Validationf(
			"apply_patch: absolute path %q is not allowed", p)
	}
	clean := path.Clean(slashed)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return errdefs.Validationf(
			"apply_patch: path %q escapes the workspace", p)
	}
	return nil
}

// Apply applies parsed ops to a workspace.
func Apply(ctx context.Context, ws workspace.Workspace, ops []*op) ([]FileResult, error) {
	var results []FileResult
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.kind {
		case opAdd:
			exists, err := ws.Exists(ctx, op.path)
			if err != nil {
				return results, err
			}
			if exists {
				return results, errdefs.Conflictf(
					"apply_patch: file %q already exists", op.path)
			}
			content := strings.Join(op.body, "\n") + "\n"
			if err := ws.Write(ctx, op.path, []byte(content)); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "add"})
		case opUpdate:
			if err := applyUpdate(ctx, ws, op); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "update"})
		case opDelete:
			exists, err := ws.Exists(ctx, op.path)
			if err != nil {
				return results, err
			}
			if !exists {
				return results, errdefs.NotFoundf(
					"apply_patch: file %q does not exist", op.path)
			}
			if err := ws.Delete(ctx, op.path); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "delete"})
		}
	}
	return results, nil
}

func applyUpdate(ctx context.Context, ws workspace.Workspace, op *op) error {
	data, err := ws.Read(ctx, op.path)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || os.IsNotExist(err) {
			return errdefs.NotFoundf(
				"apply_patch: file %q does not exist", op.path)
		}
		return err
	}
	lines := splitLines(string(data))
	for i, h := range op.hunks {
		next, found := applyHunk(lines, h)
		if !found {
			return errdefs.Validationf(
				"apply_patch: hunk %d in %q did not match: %s",
				i+1, op.path, hunkProblem(h, lines, i))
		}
		lines = next
	}
	return ws.Write(ctx, op.path, []byte(strings.Join(lines, "\n")+"\n"))
}

// hunkProblem explains why a hunk could not be located in terms a
// model can act on. Naming only the anchor is useless for the common
// failure - the context lines the model guessed from memory do not
// match the file any more - and it says nothing at all for an
// insertion hunk that carries no anchor.
//
// The second half is what saves the round: the hunk's own lines are
// looked up in the file and the file is quoted around them, so the
// retry can be written from text the model has just seen instead of
// from another guess. When no line of the hunk resembles the file at
// all, the error asks for a re-read instead of pretending to help.
//
// applied is how many hunks of the same operation already went in:
// lines is then that intermediate file rather than the one on disk, and
// the numbers a re-read returns would not line up with the quoted ones.
func hunkProblem(h hunk, lines []string, applied int) string {
	loc := locateHunk(lines, h)
	var b strings.Builder
	b.WriteString(hunkDiagnosis(h, loc))
	if applied > 0 {
		b.WriteString("\n  (line numbers are the file's after the hunks " +
			"before this one were applied)")
	}
	if tail := loc.render(h, lines); tail != "" {
		b.WriteString("\n")
		b.WriteString(tail)
		return b.String()
	}
	b.WriteString("\n  re-read the file and copy the exact lines.")
	return b.String()
}

// hunkDiagnosis names the reason the hunk could not be located. It
// reads the locator, not the hunk alone: "no match for the first
// context line" is untrue when every line of the hunk is in the file
// and only the indentation moved, which is the case the locator exists
// to tell apart. hunkProblem appends the retry instruction, which
// depends on the same finding.
func hunkDiagnosis(h hunk, loc hunkLocation) string {
	switch {
	case h.unified && len(h.removed) == 0 && h.atLine > 0:
		return fmt.Sprintf(
			"the file has fewer than %d lines, so the insertion point "+
				"from the hunk header could not be used", h.atLine)
	case len(h.removed) == 0 && h.anchor == "":
		return "insertion hunk has no anchor and no context lines to " +
			"locate it; add surrounding context lines or a non-empty " +
			"\"@@\" anchor"
	case len(h.removed) == 0:
		return fmt.Sprintf(
			"no line contains the \"@@\" anchor %q", h.anchor)
	case loc.whole():
		return fmt.Sprintf(
			"every line of the hunk is in the file from line %d, but "+
				"with different leading or trailing space", loc.start()+1)
	case loc.found():
		return "the hunk's lines no longer line up in the file"
	default:
		return fmt.Sprintf(
			"none of the hunk's lines is in the file any more, "+
				"starting with %q", clipLine(h.removed[0]))
	}
}

// hunkWindowRadius is how many file lines the locator quotes on each
// side of the line it found. Three matches what a hunk usually carries
// as context, so the window holds the lines a retry needs.
const hunkWindowRadius = 3

// hunkLocation is where a hunk's own lines do appear in the file.
type hunkLocation struct {
	at     int // file index of the located line; -1 when nothing matched
	hunkAt int // index within the hunk of the line sitting on at
	agree  int // hunk lines that line up there
	total  int // hunk lines the locator compared
}

func (l hunkLocation) found() bool { return l.at >= 0 }

// whole reports that every line of the hunk is in the file. The
// comparison ignores surrounding space, so when this holds and the hunk
// still did not apply, that space is the whole difference.
func (l hunkLocation) whole() bool { return l.found() && l.agree == l.total }

// start is the file index of the hunk's first line. That is where the
// reader has to look: the anchor line the locator settled on can sit
// anywhere inside the hunk.
func (l hunkLocation) start() int { return l.at - l.hunkAt }

// render quotes where the hunk's own lines do appear in the file, and
// what the file has no line for. It returns "" when none of them
// resembles the file - a region rewritten past recognition - which
// leaves the caller to ask for a re-read.
func (l hunkLocation) render(h hunk, lines []string) string {
	if !l.found() || len(lines) == 0 || len(h.removed) == 0 {
		// Nothing of the hunk is text the file must contain: an insertion
		// hunk locates itself by anchor or by header, and an empty file has
		// no place to quote.
		return ""
	}
	var b strings.Builder
	if l.whole() {
		// The window opens on the hunk's first line, so the line it marks
		// is the one the diagnosis names.
		b.WriteString(renderWindow(lines, l.start()))
	} else {
		fmt.Fprintf(&b,
			"  hunk line %d is at line %d of %d, and %d of %d hunk "+
				"lines match there:\n", l.hunkAt+1, l.at+1, len(lines),
			l.agree, l.total)
		b.WriteString(renderWindow(lines, l.at))
	}
	if missing := missingHunkLines(h, lines); len(missing) > 0 {
		fmt.Fprintf(&b, "  the file has no line for: %s\n",
			strings.Join(missing, ", "))
	}
	if l.whole() {
		b.WriteString("  re-send the hunk with the file's own whitespace.")
	} else {
		b.WriteString("  re-send the hunk with the lines above.")
	}
	return b.String()
}

// locateHunk finds where the hunk's lines do appear, which is the
// position that lines up the most of them, compared
// whitespace-insensitively because indentation that moved is the common
// failure.
func locateHunk(lines []string, h hunk) hunkLocation {
	loc := hunkLocation{at: -1, hunkAt: -1, total: len(h.removed)}
	bestLen, bestExact := 0, false
	for k, want := range h.removed {
		i, exact := indexLine(lines, want)
		if i < 0 {
			continue
		}
		n := countAgree(lines, h, i, k)
		key := len(strings.TrimSpace(want))
		// Rank by agreement, then by how specific the anchoring line
		// is, then by an exact match: a lone "}" is a poor locator.
		if !better(n, key, exact, loc.agree, bestLen, bestExact) {
			continue
		}
		loc.at, loc.hunkAt, loc.agree = i, k, n
		bestLen, bestExact = key, exact
	}
	return loc
}

// better reports whether one locator candidate outranks another.
func better(n, key int, exact bool, bestN, bestKey int, bestExact bool) bool {
	if n != bestN {
		return n > bestN
	}
	if key != bestKey {
		return key > bestKey
	}
	return exact && !bestExact
}

// countAgree counts the hunk lines that line up around at, with hunk
// line hunkAt sitting on it.
func countAgree(lines []string, h hunk, at, hunkAt int) int {
	n := 0
	for j, want := range h.removed {
		i := at + (j - hunkAt)
		if i < 0 || i >= len(lines) {
			continue
		}
		if strings.TrimSpace(lines[i]) == strings.TrimSpace(want) {
			n++
		}
	}
	return n
}

// indexLine returns the position of want in lines, preferring a byte
// for byte match over one that only differs in surrounding space.
func indexLine(lines []string, want string) (int, bool) {
	for i, line := range lines {
		if line == want {
			return i, true
		}
	}
	key := strings.TrimSpace(want)
	if key == "" {
		return -1, false
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == key {
			return i, false
		}
	}
	return -1, false
}

// missingHunkLines quotes the hunk lines the file has nowhere, up to
// three of them: a hunk that was written against an older version of
// the file fails on exactly these.
func missingHunkLines(h hunk, lines []string) []string {
	have := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		have[strings.TrimSpace(line)] = struct{}{}
	}
	var out []string
	for _, want := range h.removed {
		if _, ok := have[strings.TrimSpace(want)]; ok {
			continue
		}
		if len(out) == 3 {
			return append(out, "...")
		}
		out = append(out, fmt.Sprintf("%q", clipLine(want)))
	}
	return out
}

// renderWindow quotes the file around at, numbered and with the located
// line marked, so a hunk can be re-issued from what is shown here.
func renderWindow(lines []string, at int) string {
	if at < 0 || at >= len(lines) {
		return ""
	}
	lo, hi := at-hunkWindowRadius, at+hunkWindowRadius
	if lo < 0 {
		lo = 0
	}
	if hi > len(lines)-1 {
		hi = len(lines) - 1
	}
	var b strings.Builder
	for i := lo; i <= hi; i++ {
		marker := "  "
		if i == at {
			marker = "> "
		}
		fmt.Fprintf(&b, "  %s%5d | %s\n", marker, i+1, clipLine(lines[i]))
	}
	return b.String()
}

// clipLine bounds one quoted line so a minified bundle cannot turn the
// error into a second problem.
func clipLine(line string) string {
	const max = 200
	if len(line) <= max {
		return line
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + "..."
}

// applyHunk replaces the first match of removed lines with added lines.
// Insertion hunks (no removed lines) use the @@ anchor to find position.
func applyHunk(lines []string, h hunk) ([]string, bool) {
	if len(h.removed) == 0 {
		idx := -1
		if h.anchor != "" {
			for i, line := range lines {
				if strings.Contains(line, h.anchor) {
					idx = i
					break
				}
			}
		} else if h.unified && h.atLine > 0 {
			// Unified-diff insertion: "@@ -5,0 +6,2 @@" inserts after
			// old line 5. Clamp to the file so a stale number still lands
			// somewhere sensible instead of failing the whole patch.
			idx = h.atLine - 1
			if idx > len(lines)-1 {
				idx = len(lines) - 1
			}
		}
		if idx < 0 {
			// Insert at the top when the header asked for line 0.
			if h.unified && h.atLine == 0 && h.anchor == "" {
				out := make([]string, 0, len(lines)+len(h.added))
				out = append(out, h.added...)
				out = append(out, lines...)
				return out, true
			}
			return lines, false
		}
		out := make([]string, 0, len(lines)+len(h.added))
		out = append(out, lines[:idx+1]...)
		out = append(out, h.added...)
		out = append(out, lines[idx+1:]...)
		return out, true
	}
	for i := 0; i+len(h.removed) <= len(lines); i++ {
		match := true
		for j := range h.removed {
			if lines[i+j] != h.removed[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		out := make([]string, 0, len(lines)-len(h.removed)+len(h.added))
		out = append(out, lines[:i]...)
		out = append(out, h.added...)
		out = append(out, lines[i+len(h.removed):]...)
		return out, true
	}
	return lines, false
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// FileResult describes one applied file operation.
type FileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}
