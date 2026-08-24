package patch

import (
	"os"
	"strings"
)

// DiffLineKind classifies one rendered diff line.
type DiffLineKind int

const (
	// DiffLineContext is an unchanged line shown for positioning.
	DiffLineContext DiffLineKind = iota
	// DiffLineAdd is a line added by the patch.
	DiffLineAdd
	// DiffLineDelete is a line removed by the patch.
	DiffLineDelete
)

// DiffLine is one diff line with the file line numbers it maps to.
// OldNum is the line number in the file before the patch (0 when not
// applicable, e.g. an added line), NewNum the number after (0 for a
// deleted line).
type DiffLine struct {
	Kind   DiffLineKind
	OldNum int
	NewNum int
	Text   string
}

// FileDiff is the rendered diff of one file operation.
type FileDiff struct {
	Path    string
	Action  string // "add" | "update" | "delete"
	Added   int
	Removed int
	Lines   []DiffLine
}

// ReadFile reads a workspace file as text. Paths are relative to the
// workspace root.
type ReadFile func(path string) (string, error)

// Diff parses a codex-format patch and renders every file operation as
// diff lines with line numbers. readFile supplies the current file
// content (used to compute real line numbers for update/delete
// operations); when a file cannot be read the lines still render, with
// numbers falling back to hunk-relative positions.
func Diff(patch string, readFile ReadFile) ([]FileDiff, error) {
	if _, err := Parse(patch); err != nil {
		return nil, err
	}
	if readFile == nil {
		readFile = func(string) (string, error) {
			return "", os.ErrNotExist
		}
	}
	ops := parsePatch(patch)
	out := make([]FileDiff, 0, len(ops))
	for _, op := range ops {
		fd := FileDiff{Path: op.path, Action: op.kind.String()}
		switch op.kind {
		case opAdd:
			for i, line := range op.body {
				fd.Lines = append(fd.Lines, DiffLine{
					Kind: DiffLineAdd, NewNum: i + 1, Text: line,
				})
				fd.Added++
			}
		case opDelete:
			if data, err := readFile(op.path); err == nil {
				for i, line := range splitLines(data) {
					fd.Lines = append(fd.Lines, DiffLine{
						Kind: DiffLineDelete, OldNum: i + 1, Text: line,
					})
					fd.Removed++
				}
			}
		case opUpdate:
			renderUpdate(&fd, op, readFile)
		}
		out = append(out, fd)
	}
	return out, nil
}

func (k opKind) String() string {
	switch k {
	case opAdd:
		return "add"
	case opDelete:
		return "delete"
	default:
		return "update"
	}
}

// patchOp keeps the full ordering of hunk lines, which Parse drops
// (its hunk model stores removed and added in parallel slices).
type patchOp struct {
	kind  opKind
	path  string
	body  []string    // add
	hunks []patchHunk // update
}

type patchHunk struct {
	anchor string
	lines  []diffEntry
}

type diffEntry struct {
	kind DiffLineKind
	text string
}

// parsePatch re-parses the patch text into ordered ops. The patch has
// already been validated by Parse, so this only needs to classify
// lines; unknown shapes are skipped defensively.
func parsePatch(patch string) []*patchOp {
	lines := strings.Split(patch, "\n")
	var ops []*patchOp
	var current *patchOp
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "*** Begin Patch", trimmed == "*** End Patch":
			current = nil
		case strings.HasPrefix(trimmed, "*** Add File: "):
			current = &patchOp{
				kind: opAdd,
				path: strings.TrimSpace(strings.TrimPrefix(
					trimmed, "*** Add File: ")),
			}
			ops = append(ops, current)
		case strings.HasPrefix(trimmed, "*** Update File: "):
			current = &patchOp{
				kind: opUpdate,
				path: strings.TrimSpace(strings.TrimPrefix(
					trimmed, "*** Update File: ")),
			}
			ops = append(ops, current)
		case strings.HasPrefix(trimmed, "*** Delete File: "):
			current = &patchOp{
				kind: opDelete,
				path: strings.TrimSpace(strings.TrimPrefix(
					trimmed, "*** Delete File: ")),
			}
			ops = append(ops, current)
		case current == nil:
			continue
		case current.kind == opAdd:
			if strings.HasPrefix(line, "+") {
				current.body = append(current.body,
					strings.TrimPrefix(line, "+"))
			}
		case current.kind == opUpdate:
			switch {
			case strings.HasPrefix(line, "@@"):
				current.hunks = append(current.hunks, patchHunk{
					anchor: strings.TrimSpace(
						strings.TrimPrefix(line, "@@")),
				})
			case len(current.hunks) == 0:
				continue
			case strings.HasPrefix(line, "-"):
				h := &current.hunks[len(current.hunks)-1]
				h.lines = append(h.lines, diffEntry{
					kind: DiffLineDelete,
					text: strings.TrimPrefix(line, "-"),
				})
			case strings.HasPrefix(line, "+"):
				h := &current.hunks[len(current.hunks)-1]
				h.lines = append(h.lines, diffEntry{
					kind: DiffLineAdd,
					text: strings.TrimPrefix(line, "+"),
				})
			case strings.HasPrefix(line, " "):
				h := &current.hunks[len(current.hunks)-1]
				h.lines = append(h.lines, diffEntry{
					kind: DiffLineContext,
					text: strings.TrimPrefix(line, " "),
				})
			}
		}
	}
	return ops
}

// renderUpdate renders update hunks against the real file content,
// applying each hunk so the next one computes against the updated
// lines. Hunks that cannot be located fall back to relative numbers.
func renderUpdate(fd *FileDiff, op *patchOp, readFile ReadFile) {
	cur, err := readFile(op.path)
	lines := splitLines(cur)
	haveFile := err == nil
	for _, h := range op.hunks {
		start := -1
		if haveFile {
			start = findHunkStart(lines, h)
		}
		if start >= 0 {
			if isInsertionHunk(h) {
				// Insertion hunks place lines right after the anchor
				// line, so the first added line gets anchor+1.
				for i, e := range h.lines {
					fd.Lines = append(fd.Lines, DiffLine{
						Kind: DiffLineAdd, NewNum: start + 2 + i,
						Text: e.text,
					})
					fd.Added++
				}
				lines = applyHunkAt(lines, start, h)
				continue
			}
			old, next := start+1, start+1
			for _, e := range h.lines {
				switch e.kind {
				case DiffLineContext:
					fd.Lines = append(fd.Lines, DiffLine{
						Kind: DiffLineContext, OldNum: old,
						NewNum: next, Text: e.text,
					})
					old++
					next++
				case DiffLineDelete:
					fd.Lines = append(fd.Lines, DiffLine{
						Kind: DiffLineDelete, OldNum: old, Text: e.text,
					})
					fd.Removed++
					old++
				case DiffLineAdd:
					fd.Lines = append(fd.Lines, DiffLine{
						Kind: DiffLineAdd, NewNum: next, Text: e.text,
					})
					fd.Added++
					next++
				}
			}
			lines = applyHunkAt(lines, start, h)
			continue
		}
		// Fallback: hunk-relative numbering keeps the diff readable.
		old, next := 1, 1
		for _, e := range h.lines {
			switch e.kind {
			case DiffLineContext:
				fd.Lines = append(fd.Lines, DiffLine{
					Kind: DiffLineContext, OldNum: old,
					NewNum: next, Text: e.text,
				})
				old++
				next++
			case DiffLineDelete:
				fd.Lines = append(fd.Lines, DiffLine{
					Kind: DiffLineDelete, OldNum: old, Text: e.text,
				})
				fd.Removed++
				old++
			case DiffLineAdd:
				fd.Lines = append(fd.Lines, DiffLine{
					Kind: DiffLineAdd, NewNum: next, Text: e.text,
				})
				fd.Added++
				next++
			}
		}
	}
}

// isInsertionHunk reports whether a hunk only adds lines (no context
// or removed lines), in which case the @@ anchor locates the insertion
// point.
func isInsertionHunk(h patchHunk) bool {
	for _, e := range h.lines {
		if e.kind != DiffLineAdd {
			return false
		}
	}
	return len(h.lines) > 0
}

// findHunkStart locates a hunk in lines. Removal hunks match the full
// context+removed sequence; insertion hunks locate the @@ anchor.
func findHunkStart(lines []string, h patchHunk) int {
	var removed []string
	for _, e := range h.lines {
		if e.kind != DiffLineAdd {
			removed = append(removed, e.text)
		}
	}
	if len(removed) == 0 {
		if h.anchor == "" {
			return -1
		}
		for i, line := range lines {
			if strings.Contains(line, h.anchor) {
				return i
			}
		}
		return -1
	}
	for i := 0; i+len(removed) <= len(lines); i++ {
		ok := true
		for j := range removed {
			if lines[i+j] != removed[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// applyHunkAt applies h at the known start position, producing the
// lines the next hunk must match against.
func applyHunkAt(lines []string, start int, h patchHunk) []string {
	var removed, added []string
	for _, e := range h.lines {
		switch e.kind {
		case DiffLineAdd:
			added = append(added, e.text)
		default:
			removed = append(removed, e.text)
		}
	}
	out := make([]string, 0,
		len(lines)-len(removed)+len(added))
	out = append(out, lines[:start]...)
	out = append(out, added...)
	out = append(out, lines[start+len(removed):]...)
	return out
}
