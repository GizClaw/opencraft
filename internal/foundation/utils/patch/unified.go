package patch

import (
	"strconv"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// This file teaches the patch engine to *read* standard unified diffs
// (`git diff` output), while the codex envelope remains the format the
// model is asked to write. The two inputs converge on the same op model,
// so applying, rendering and safety checks stay in one place: a hunk is
// still located by matching its content against the file, and the line
// numbers in the header are only a fallback for hunks that carry no
// context at all.

// LooksLikeUnifiedDiff reports whether the text is a standard unified
// diff rather than a codex-envelope patch. It keys on the file headers
// git emits, so an envelope patch (which starts with "*** Begin Patch")
// is never mistaken for one.
func LooksLikeUnifiedDiff(text string) bool {
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "diff --git ") {
			return true
		}
		// A bare "--- a/x" followed by "+++ b/x" is the minimal header
		// pair `git diff` prints when there is no index line.
		if strings.HasPrefix(line, "--- ") && i+1 < len(lines) &&
			strings.HasPrefix(strings.TrimRight(lines[i+1], "\r"), "+++ ") {
			return true
		}
	}
	return false
}

// parseUnifiedDiffOps converts a unified diff into the op list the
// apply/render paths consume.
func parseUnifiedDiffOps(text string) ([]*op, error) {
	lines := strings.Split(text, "\n")
	var ops []*op
	var current *op
	var currentHunk *hunk
	var sawHunkContent bool
	// oldPath/newPath come from the ---/+++ pair; git puts /dev/null on
	// the side that did not exist.
	oldPath, newPath := "", ""

	flushHunk := func() {
		if current != nil && currentHunk != nil && sawHunkContent {
			current.hunks = append(current.hunks, *currentHunk)
		}
		currentHunk = nil
		sawHunkContent = false
	}

	for i := range lines {
		raw := strings.TrimRight(lines[i], "\r")
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			flushHunk()
			oldPath, newPath = parseDiffGitPaths(raw)
			current = &op{kind: opUpdate, path: newPath}
			if current.path == "" {
				current.path = oldPath
			}
			ops = append(ops, current)
		case strings.HasPrefix(raw, "--- "):
			flushHunk()
			oldPath = cleanDiffPath(strings.TrimPrefix(raw, "--- "))
			if current == nil {
				current = &op{kind: opUpdate, path: oldPath}
				ops = append(ops, current)
			}
		case strings.HasPrefix(raw, "+++ "):
			newPath = cleanDiffPath(strings.TrimPrefix(raw, "+++ "))
			if current == nil {
				current = &op{kind: opUpdate, path: newPath}
				ops = append(ops, current)
			}
			switch {
			case oldPath == "/dev/null":
				current.kind = opAdd
				current.path = newPath
			case newPath == "/dev/null":
				current.kind = opDelete
				current.path = oldPath
			default:
				current.kind = opUpdate
				current.path = newPath
			}
		case strings.HasPrefix(raw, "new file mode"):
			if current != nil {
				current.kind = opAdd
			}
		case strings.HasPrefix(raw, "deleted file mode"):
			if current != nil {
				current.kind = opDelete
			}
		case strings.HasPrefix(raw, "rename from "),
			strings.HasPrefix(raw, "rename to "),
			strings.HasPrefix(raw, "copy from "),
			strings.HasPrefix(raw, "copy to "):
			return nil, errdefs.Validationf(
				"apply_patch: rename/copy hunks are not supported; "+
					"express it as a delete plus an add (%q)", raw)
		case strings.HasPrefix(raw, "Binary files "):
			return nil, errdefs.Validationf(
				"apply_patch: binary diffs are not supported (%q)", raw)
		case strings.HasPrefix(raw, "@@"):
			flushHunk()
			if current == nil {
				return nil, errdefs.Validationf(
					"apply_patch: hunk header before any file header: %q", raw)
			}
			start, count, err := parseHunkRange(raw)
			if err != nil {
				return nil, err
			}
			currentHunk = &hunk{unified: true}
			if count == 0 {
				// "@@ -5,0 +6,2 @@" is a pure insertion after line 5:
				// there is no context to match, so remember the position.
				currentHunk.atLine = start
			}
		case current == nil:
			// Preamble (index lines, mode lines, mail headers) before the
			// first file header.
			continue
		case strings.HasPrefix(raw, " "):
			if currentHunk == nil {
				return nil, errdefs.Validationf(
					"apply_patch: content line outside a hunk: %q", raw)
			}
			currentHunk.removed = append(currentHunk.removed, raw[1:])
			currentHunk.added = append(currentHunk.added, raw[1:])
			currentHunk.entries = append(currentHunk.entries, hunkEntry{
				kind: DiffLineContext, text: raw[1:],
			})
			sawHunkContent = true
		case strings.HasPrefix(raw, "-"):
			if currentHunk == nil {
				return nil, errdefs.Validationf(
					"apply_patch: content line outside a hunk: %q", raw)
			}
			currentHunk.removed = append(currentHunk.removed, raw[1:])
			currentHunk.entries = append(currentHunk.entries, hunkEntry{
				kind: DiffLineDelete, text: raw[1:],
			})
			sawHunkContent = true
		case strings.HasPrefix(raw, "+"):
			if currentHunk == nil {
				return nil, errdefs.Validationf(
					"apply_patch: content line outside a hunk: %q", raw)
			}
			currentHunk.added = append(currentHunk.added, raw[1:])
			currentHunk.entries = append(currentHunk.entries, hunkEntry{
				kind: DiffLineAdd, text: raw[1:],
			})
			sawHunkContent = true
		case strings.HasPrefix(raw, `\`):
			// "\ No newline at end of file": metadata, never content.
			continue
		case raw == "":
			continue
		default:
			// Anything else (index lines, mode changes) is metadata.
			continue
		}
	}
	flushHunk()

	if len(ops) == 0 {
		return nil, errdefs.Validationf(
			"apply_patch: no file headers found in the unified diff")
	}
	for _, op := range ops {
		if op.path == "" || op.path == "/dev/null" {
			return nil, errdefs.Validationf(
				"apply_patch: unified diff has a file without a path")
		}
		if err := validatePath(op.path); err != nil {
			return nil, err
		}
		if op.kind == opAdd && len(op.body) == 0 && len(op.hunks) == 0 {
			return nil, errdefs.Validationf(
				"apply_patch: add %q has no lines", op.path)
		}
	}
	// For adds the body is what the renderer and the writer consume; move
	// the hunk's added lines there and keep uniform "add" semantics.
	for _, op := range ops {
		if op.kind != opAdd {
			continue
		}
		for _, h := range op.hunks {
			op.body = append(op.body, h.added...)
		}
		op.hunks = nil
	}
	return ops, nil
}

// parseDiffGitPaths reads the two paths from a "diff --git a/x b/y"
// header. Paths may contain spaces, so the two halves are matched by
// their a/ and b/ prefixes rather than by splitting on whitespace.
func parseDiffGitPaths(line string) (string, string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	// Quoted paths ("a/x y") are emitted when the name has spaces; strip
	// the quotes so the path matches the filesystem.
	rest = strings.ReplaceAll(rest, `"`, "")
	idx := strings.Index(rest, " b/")
	if idx < 0 {
		return cleanDiffPath(rest), ""
	}
	return cleanDiffPath(rest[:idx]), cleanDiffPath(rest[idx+1:])
}

// cleanDiffPath strips the a/ b/ prefixes and any trailing tab metadata
// ("--- a/x\t2020-01-01 00:00:00"), and normalizes /dev/null.
func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if tab := strings.IndexByte(path, '\t'); tab >= 0 {
		path = path[:tab]
	}
	path = strings.TrimSpace(path)
	if path == "/dev/null" {
		return path
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return strings.TrimSpace(path)
}

// parseHunkRange parses "@@ -12,4 +12,6 @@ optional context" into the
// old start line and old line count.
func parseHunkRange(line string) (start, count int, err error) {
	rest := strings.TrimPrefix(line, "@@")
	end := strings.Index(rest, "@@")
	if end < 0 {
		return 0, 0, errdefs.Validationf(
			"apply_patch: malformed hunk header %q", line)
	}
	field := strings.Fields(rest[:end])
	if len(field) == 0 || !strings.HasPrefix(field[0], "-") {
		return 0, 0, errdefs.Validationf(
			"apply_patch: malformed hunk header %q", line)
	}
	rangeText := strings.TrimPrefix(field[0], "-")
	startText, countText := rangeText, "1"
	if comma := strings.IndexByte(rangeText, ','); comma >= 0 {
		startText, countText = rangeText[:comma], rangeText[comma+1:]
	}
	start, err = strconv.Atoi(strings.TrimSpace(startText))
	if err != nil {
		return 0, 0, errdefs.Validationf(
			"apply_patch: malformed hunk start in %q", line)
	}
	count, err = strconv.Atoi(strings.TrimSpace(countText))
	if err != nil {
		return 0, 0, errdefs.Validationf(
			"apply_patch: malformed hunk length in %q", line)
	}
	if start < 0 || count < 0 {
		return 0, 0, errdefs.Validationf(
			"apply_patch: negative hunk range in %q", line)
	}
	return start, count, nil
}
