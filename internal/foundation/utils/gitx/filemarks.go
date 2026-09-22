// Line-level change marks for one file: the new-side coordinates of
// every difference between HEAD and the working tree, which is exactly
// what a file preview draws a gutter from.
//
// The base is HEAD (`git diff HEAD`), never the index on its own. The
// preview shows the working tree, so its line numbers are the only
// coordinates a gutter can use: `git diff --cached` numbers hunks
// against the index, and every unstaged insertion above a staged change
// shifts them. Merging the two sides in Go would mean re-projecting the
// staged hunks through the unstaged ones; asking git for the combined
// diff gets the same answer for one process.
//
// Like the rest of the package this is read-only and bounded: it never
// mutates the repository, never runs a write command, and every output
// has a byte cap.
package gitx

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The bounds are variables rather than constants so the tests can
// shrink them and reach the truncation paths with a small repository.
var (
	// emptyTreeOID is git's well-known hash of the empty tree. It is the
	// diff base in a repository without commits, where HEAD cannot be
	// resolved at all.
	emptyTreeOID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	// marksStatusLimit bounds the status snapshot a marks query reads.
	marksStatusLimit int64 = 4 << 20
	// marksStatusPaths bounds how many status entries that snapshot
	// keeps before it is treated as truncated.
	marksStatusPaths = 2000
	// marksDiffLimit bounds one file's diff. The preview caps text at
	// 2 MiB, and a full rewrite can double that in patch lines.
	marksDiffLimit int64 = 4 << 20
	// marksMaxRanges bounds the returned ranges so a pathological file
	// (every second line replaced) cannot cross the desktop bridge as a
	// huge array. Past the cap the caller keeps the kind and the counts
	// and skips the marks.
	marksMaxRanges = 4096
)

// LineRange is one run of new-file lines, 1-based and half-open:
// [Start, Start+Count).
type LineRange struct {
	Start int
	Count int
}

// DeleteAnchor marks a gap where lines were deleted. The new file has
// no line to attach it to, so the caller draws the mark at the gap:
// After is the number of unchanged new-file lines above it (0 puts the
// gap above line 1) and Count is how many base-version lines vanished.
type DeleteAnchor struct {
	After    int
	Count    int
	OldStart int
}

// MarkSet is the line-level change snapshot of one file against HEAD.
type MarkSet struct {
	// Path is the repo-relative, slash-separated path.
	Path string
	// OrigPath is the rename/copy source when Kind is renamed/copied.
	OrigPath string
	// Kind is empty for a clean (or ignored) file.
	Kind ChangeKind
	// Staged/Unstaged mark which index columns reported the path.
	Staged   bool
	Unstaged bool
	// Untracked marks a file git does not know yet; Unmerged marks a
	// conflict. Both report no ranges.
	Untracked bool
	Unmerged  bool
	// Binary is true when git reports the change as binary.
	Binary bool
	// Truncated is true when the ranges were dropped: the diff or the
	// range list exceeded its bound.
	Truncated bool
	// Additions/Deletions count changed text lines, the same way
	// `git diff --numstat` does.
	Additions int
	Deletions int
	// Adds and Mods are new-side line ranges (inserted and replaced).
	Adds []LineRange
	Mods []LineRange
	// Dels are deletion gaps described by their new-side position.
	Dels []DeleteAnchor
}

// FileMarks returns the line marks for one repo-relative path. A file
// that is not in the repository (clean, ignored, or outside it) reports
// an empty Kind and no ranges instead of an error, so the caller can
// render "no marks" without special-casing a failure.
func FileMarks(ctx context.Context, root, path string) MarkSet {
	if root == "" || path == "" || !isSafePath(path) {
		return MarkSet{}
	}
	path = filepath.ToSlash(path)
	entry, ok := marksEntry(ctx, root, path)
	if !ok {
		return MarkSet{Path: path}
	}
	marks := MarkSet{
		Path:      entry.Path,
		OrigPath:  entry.OrigPath,
		Kind:      entry.Kind,
		Staged:    entry.Staged,
		Unstaged:  entry.Unstaged,
		Untracked: entry.Untracked,
		Unmerged:  entry.Unmerged,
	}
	// Untracked files (and conflicts) have no diff against HEAD worth
	// drawing: there is no base version to compare line by line.
	if entry.Untracked || entry.Unmerged || entry.Directory {
		return marks
	}
	base := headOID(ctx, root)
	if base == "" {
		base = emptyTreeOID
	}
	args := []string{
		"-c", "core.quotepath=false",
		"diff", "--no-color", "-U0", base,
		"--", literalSpec(path),
	}
	if entry.OrigPath != "" {
		// A rename has to be diffed with both sides in the pathspec:
		// limited to the destination alone git cannot pair the two and
		// reports the file as brand new (every line marked added).
		args = append(args, literalSpec(entry.OrigPath))
	}
	out, truncated := RunBounded(ctx, root, marksDiffLimit, 15*time.Second, args...)
	if truncated {
		marks.Truncated = true
		return marks
	}
	if isBinaryDiff(out) {
		marks.Binary = true
		return marks
	}
	marks.Adds, marks.Mods, marks.Dels, marks.Additions, marks.Deletions =
		parseHunks(out)
	if len(marks.Adds)+len(marks.Mods)+len(marks.Dels) > marksMaxRanges {
		marks.Truncated = true
		marks.Adds, marks.Mods, marks.Dels = nil, nil, nil
	}
	return marks
}

// marksEntry locates path in the change snapshot. The snapshot is the
// whole-repository one (not a path-limited query) because the rename
// pairing above needs the source side, which a pathspec of one path
// hides. A snapshot that dropped entries to its caps falls back to a
// path-limited query so a busy repository cannot make a changed file
// read as clean.
func marksEntry(ctx context.Context, root, path string) (Entry, bool) {
	entries, truncated := statusEntries(
		ctx, root, marksStatusLimit, marksStatusPaths)
	for _, e := range entries {
		if e.Path == path {
			return e, true
		}
	}
	if !truncated {
		return Entry{}, false
	}
	out, _ := RunBounded(ctx, root, marksStatusLimit, 15*time.Second,
		"-c", "core.quotepath=false",
		"status", "--porcelain=v2", "-z", "--untracked-files=normal",
		"--", literalSpec(path))
	if out == "" {
		return Entry{}, false
	}
	for _, e := range parsePorcelainV2(out, 1) {
		if e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

// headOID resolves HEAD, returning "" in a repository without commits.
func headOID(ctx context.Context, root string) string {
	out, _ := RunBounded(ctx, root, 4<<10, 5*time.Second,
		"rev-parse", "--verify", "-q", "HEAD")
	return strings.TrimSpace(out)
}

// literalSpec renders one path as a literal pathspec. Without the
// `:(literal)` magic a path containing glob characters (a Next.js
// `[slug].tsx`, for one) is matched as a pattern and the file's own
// changes go unnoticed.
func literalSpec(path string) string {
	return ":(literal)" + path
}

// isBinaryDiff reports whether git answered with a binary marker
// instead of text hunks.
func isBinaryDiff(diff string) bool {
	return strings.Contains(diff, "Binary files ") ||
		strings.Contains(diff, "GIT binary patch")
}

// hunkHeaderRe matches the header of one unified-diff hunk. Both counts
// are optional (git omits them when they are 1) and everything after
// the closing `@@` is the function-context hint, which is ignored.
var hunkHeaderRe = regexp.MustCompile(
	`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// parseHunks splits a `-U0` diff into new-side line ranges, deletion
// anchors and line counts.
func parseHunks(diff string) (
	adds, mods []LineRange,
	dels []DeleteAnchor,
	additions, deletions int,
) {
	for _, line := range strings.Split(diff, "\n") {
		m := hunkHeaderRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		oldStart, oldCount := hunkCount(m[1], m[2])
		newStart, newCount := hunkCount(m[3], m[4])
		switch {
		case oldCount == 0:
			// Pure insertion: the new lines are all added.
			adds = append(adds, LineRange{Start: newStart, Count: newCount})
			additions += newCount
		case newCount == 0:
			// Pure deletion: nothing to mark in the new file, so the
			// gap itself carries the count.
			dels = append(dels, DeleteAnchor{
				After:    newStart,
				Count:    oldCount,
				OldStart: oldStart,
			})
			deletions += oldCount
		default:
			mods = append(mods, LineRange{Start: newStart, Count: newCount})
			additions += newCount
			deletions += oldCount
		}
	}
	return adds, mods, dels, additions, deletions
}

// hunkCount decodes one hunk-side header field: the start line plus the
// optional count (1 when git leaves it out, 0 for an empty side).
func hunkCount(start, count string) (int, int) {
	n, err := strconv.Atoi(start)
	if err != nil {
		return 0, 0
	}
	if count == "" {
		return n, 1
	}
	c, err := strconv.Atoi(count)
	if err != nil {
		return n, 1
	}
	return n, c
}
