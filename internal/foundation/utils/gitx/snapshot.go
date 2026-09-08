// Read-only repository snapshots for UI panels. Everything here stays
// bounded and never mutates the repository, matching the gitx package
// contract. Parsing targets git's machine-readable outputs:
//
//   - status:  porcelain v2 with -z (rename pairs span two NUL records)
//   - counts:  diff --numstat with -z (rename pairs carry two paths)
//   - log:     custom format with unit/record separators
//   - branches: for-each-ref with NUL field separators
package gitx

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultStatusLimit bounds one porcelain status snapshot.
	defaultStatusLimit = 4 << 20
	// defaultStatusPaths bounds how many change entries a snapshot keeps.
	defaultStatusPaths = 2000
	// defaultDiffLimit bounds one unified diff payload.
	defaultDiffLimit = 2 << 20
)

// RepoInfo describes the repository that contains dir.
type RepoInfo struct {
	// Root is the repository top level; empty when dir is not inside a
	// work tree. Root may be an ancestor of dir.
	Root string
	// Branch is the abbreviated HEAD ref ("HEAD" when detached).
	Branch string
}

// Info locates the containing repository with git itself so worktrees
// and .git files are handled correctly.
func Info(ctx context.Context, dir string) RepoInfo {
	out, _ := RunBounded(ctx, dir, 4<<10, 5*time.Second,
		"rev-parse", "--is-inside-work-tree")
	if strings.TrimSpace(out) != "true" {
		return RepoInfo{}
	}
	root, _ := RunBounded(ctx, dir, 64<<10, 5*time.Second,
		"rev-parse", "--show-toplevel")
	if root == "" {
		return RepoInfo{}
	}
	branch, _ := RunBounded(ctx, root, 4<<10, 5*time.Second,
		"rev-parse", "--abbrev-ref", "HEAD")
	return RepoInfo{Root: root, Branch: strings.TrimSpace(branch)}
}

// ChangeKind classifies one changed path.
type ChangeKind string

const (
	KindModified   ChangeKind = "modified"
	KindAdded      ChangeKind = "added"
	KindDeleted    ChangeKind = "deleted"
	KindRenamed    ChangeKind = "renamed"
	KindCopied     ChangeKind = "copied"
	KindTypeChange ChangeKind = "typechange"
	KindUntracked  ChangeKind = "untracked"
	KindUnmerged   ChangeKind = "unmerged"
)

// Entry is one path reported by git status.
type Entry struct {
	// Path is the repo-relative, slash-separated path.
	Path string
	// OrigPath is the rename/copy source when Kind is renamed/copied.
	OrigPath string
	Kind     ChangeKind
	// Staged/Unstaged mark which index columns reported the path.
	Staged   bool
	Unstaged bool
	// Untracked marks a "?" porcelain entry.
	Untracked bool
	// Unmerged marks a conflict (porcelain "u" record).
	Unmerged bool
	// Directory is true for collapsed untracked directories.
	Directory bool
	// IsBinary is true when git reports the change as binary.
	IsBinary bool
	// Additions/Deletions count text lines changed (binary entries
	// report neither).
	Additions int
	Deletions int
}

// StatusOptions bounds one status snapshot.
type StatusOptions struct {
	// MaxBytes caps the porcelain and numstat outputs.
	MaxBytes int64
	// MaxEntries caps the number of returned paths. Larger snapshots
	// are truncated rather than buffered without bound.
	MaxEntries int
}

// StatusResult is one bounded status snapshot.
type StatusResult struct {
	Entries   []Entry
	Truncated bool
}

// Status returns a bounded, read-only status snapshot for root.
func Status(ctx context.Context, root string, opts StatusOptions) StatusResult {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultStatusLimit
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = defaultStatusPaths
	}
	if root == "" {
		return StatusResult{}
	}
	out, truncated := RunBounded(ctx, root, opts.MaxBytes, 10*time.Second,
		"-c", "core.quotepath=false",
		"status", "--porcelain=v2", "-z", "--untracked-files=normal")
	if out == "" {
		return StatusResult{Truncated: truncated}
	}
	entries := parsePorcelainV2(out, opts.MaxEntries)
	if len(entries) >= opts.MaxEntries {
		truncated = true
	}
	// Numeric line counts come from two bounded numstat passes. A
	// failed count pass degrades to zeroes instead of failing the
	// whole snapshot.
	counts, numTruncated := numstatCounts(ctx, root, false, opts.MaxBytes)
	cached, cachedTruncated := numstatCounts(ctx, root, true, opts.MaxBytes)
	truncated = truncated || numTruncated || cachedTruncated
	for i := range entries {
		e := &entries[i]
		if e.Untracked || e.Unmerged {
			continue
		}
		c := counts[e.Path]
		cc := cached[e.Path]
		e.IsBinary = c.binary || cc.binary
		e.Additions = c.added + cc.added
		e.Deletions = c.deleted + cc.deleted
	}
	return StatusResult{Entries: entries, Truncated: truncated}
}

// parsePorcelainV2 converts NUL-separated porcelain v2 records into
// entries. Rename/copy records carry the original path as a second
// NUL record, so the parser consumes it inline.
func parsePorcelainV2(out string, max int) []Entry {
	parts := strings.Split(out, "\x00")
	var entries []Entry
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if rec == "" {
			continue
		}
		var e Entry
		switch {
		case strings.HasPrefix(rec, "1 "):
			fields := strings.SplitN(strings.TrimPrefix(rec, "1 "), " ", 8)
			if len(fields) < 8 {
				continue
			}
			e = entryFromXY(fields[0], fields[7], "")
		case strings.HasPrefix(rec, "2 "):
			fields := strings.SplitN(strings.TrimPrefix(rec, "2 "), " ", 9)
			if len(fields) < 9 {
				continue
			}
			// The original path is the record after the NUL that
			// terminated this entry's path.
			orig := ""
			if i+1 < len(parts) && parts[i+1] != "" {
				orig = parts[i+1]
				i++
			}
			e = entryFromXY(fields[0], fields[8], orig)
		case strings.HasPrefix(rec, "u "):
			fields := strings.SplitN(strings.TrimPrefix(rec, "u "), " ", 10)
			if len(fields) < 10 {
				continue
			}
			e = Entry{
				Path:     fields[9],
				Kind:     KindUnmerged,
				Unmerged: true,
			}
		case strings.HasPrefix(rec, "? "):
			path := strings.TrimPrefix(rec, "? ")
			dir := strings.HasSuffix(path, "/")
			e = Entry{
				Path:      strings.TrimSuffix(path, "/"),
				Kind:      KindUntracked,
				Untracked: true,
				Directory: dir,
			}
		default:
			continue
		}
		if e.Path == "" {
			continue
		}
		entries = append(entries, e)
		if len(entries) >= max {
			break
		}
	}
	return entries
}

// entryFromXY decodes porcelain v2's leading XY index/worktree codes.
func entryFromXY(xy, path, orig string) Entry {
	e := Entry{Path: path, OrigPath: orig}
	if len(xy) < 2 {
		return e
	}
	x, y := xy[0], xy[1]
	e.Staged = x != '.'
	e.Unstaged = y != '.'
	code := x
	if code == '.' {
		code = y
	}
	switch code {
	case 'A':
		e.Kind = KindAdded
	case 'D':
		e.Kind = KindDeleted
	case 'R':
		e.Kind = KindRenamed
	case 'C':
		e.Kind = KindCopied
	case 'T':
		e.Kind = KindTypeChange
	case 'M':
		e.Kind = KindModified
	default:
		e.Kind = KindModified
	}
	return e
}

type numstat struct {
	added, deleted int
	binary         bool
}

// numstatCounts maps changed paths to their +/- line counts. In -z
// mode a rename record ends its add/delete columns with an empty path
// and carries the original then the destination path as the following
// two NUL records.
func numstatCounts(
	ctx context.Context,
	root string,
	cached bool,
	maxBytes int64,
) (map[string]numstat, bool) {
	args := []string{
		"-c", "core.quotepath=false",
		"diff", "--numstat", "-z",
	}
	if cached {
		args = append(args, "--cached")
	}
	out, truncated := RunBounded(ctx, root, maxBytes, 10*time.Second, args...)
	if out == "" {
		return nil, truncated
	}
	tokens := strings.Split(out, "\x00")
	counts := make(map[string]numstat)
	for i := 0; i < len(tokens); {
		tok := tokens[i]
		if tok == "" {
			i++
			continue
		}
		fields := strings.SplitN(tok, "\t", 3)
		if len(fields) < 3 {
			i++
			continue
		}
		stat := parseNumstat(fields[0], fields[1])
		path := fields[2]
		if path == "" && i+2 < len(tokens) {
			// Rename pair: destination path is the second of the two
			// following records. Counts are attributed to it.
			path = tokens[i+2]
			i += 2
		}
		i++
		if path == "" {
			continue
		}
		prev := counts[path]
		if stat.binary {
			prev.binary = true
		}
		prev.added += stat.added
		prev.deleted += stat.deleted
		counts[path] = prev
	}
	return counts, truncated
}

func parseNumstat(add, del string) numstat {
	if add == "-" || del == "-" {
		return numstat{binary: true}
	}
	a, errA := strconv.Atoi(add)
	d, errD := strconv.Atoi(del)
	if errA != nil || errD != nil {
		return numstat{}
	}
	return numstat{added: a, deleted: d}
}

// LogEntry is one row of a bounded git log.
type LogEntry struct {
	OID      string
	ShortOID string
	Author   string
	Date     string
	Subject  string
}

// Log returns the newest limit commits, newest first.
func Log(ctx context.Context, root string, limit int) ([]LogEntry, bool) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if root == "" {
		return nil, false
	}
	out, truncated := RunBounded(ctx, root, 1<<20, 10*time.Second,
		"log", "-n", strconv.Itoa(limit),
		"--format=%H%x1f%h%x1f%aN%x1f%aI%x1f%s%x1e")
	if out == "" {
		return nil, truncated
	}
	var entries []LogEntry
	for _, rec := range strings.Split(out, "\x1e") {
		if rec == "" {
			continue
		}
		fields := strings.SplitN(rec, "\x1f", 5)
		if len(fields) < 5 {
			continue
		}
		entries = append(entries, LogEntry{
			OID:      fields[0],
			ShortOID: fields[1],
			Author:   fields[2],
			Date:     fields[3],
			Subject:  fields[4],
		})
	}
	return entries, truncated
}

// Branch is one local branch row.
type Branch struct {
	Name     string
	Current  bool
	Upstream string
}

// Branches lists local branches with the current branch marked.
func Branches(ctx context.Context, root string) []Branch {
	if root == "" {
		return nil
	}
	out, _ := RunBounded(ctx, root, 1<<20, 10*time.Second,
		"for-each-ref",
		"--format=%(refname:short)%00%(HEAD)%00%(upstream:short)",
		"refs/heads")
	if out == "" {
		return nil
	}
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\x00", 3)
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		b := Branch{Name: fields[0], Current: fields[1] == "*"}
		if len(fields) == 3 {
			b.Upstream = fields[2]
		}
		branches = append(branches, b)
	}
	return branches
}

// RefInfo describes the checked-out branch and its upstream tracking
// position. Upstream is empty when the branch has no upstream; ahead
// and behind are zero then.
type RefInfo struct {
	Branch   string
	Upstream string
	Ahead    int
	Behind   int
}

// HasUnmerged reports whether the index contains any unmerged paths.
func HasUnmerged(ctx context.Context, root string) bool {
	if root == "" {
		return false
	}
	out, _ := RunBounded(ctx, root, 4<<10, 5*time.Second,
		"ls-files", "-u", "-z")
	return out != ""
}

// Ref resolves the current branch and its upstream counts.
func Ref(ctx context.Context, root string) RefInfo {
	if root == "" {
		return RefInfo{}
	}
	out, _ := RunBounded(ctx, root, 4<<10, 5*time.Second,
		"rev-parse", "--abbrev-ref", "HEAD")
	r := RefInfo{Branch: strings.TrimSpace(out)}
	upstream, _ := RunBounded(ctx, root, 4<<10, 5*time.Second,
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	r.Upstream = strings.TrimSpace(upstream)
	if r.Upstream == "" {
		return r
	}
	counts, _ := RunBounded(ctx, root, 4<<10, 5*time.Second,
		"rev-list", "--left-right", "--count",
		r.Upstream+"...HEAD")
	fields := strings.SplitN(counts, "\t", 2)
	if len(fields) == 2 {
		if behind, err := strconv.Atoi(strings.TrimSpace(fields[0])); err == nil {
			r.Behind = behind
		}
		if ahead, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil {
			r.Ahead = ahead
		}
	}
	return r
}

// Diff returns a bounded unified diff for one repo-relative path.
// cached selects the index vs the working tree. The second return
// value reports output truncation.
func Diff(
	ctx context.Context,
	root, path string,
	cached bool,
	maxBytes int64,
) (string, bool) {
	if root == "" || path == "" || !isSafePath(path) {
		return "", false
	}
	if maxBytes <= 0 {
		maxBytes = defaultDiffLimit
	}
	args := []string{
		"-c", "core.quotepath=false",
		"diff", "--no-color",
	}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--", filepath.FromSlash(path))
	out, truncated := RunBounded(ctx, root, maxBytes, 15*time.Second, args...)
	return out, truncated
}

// isSafePath rejects paths that could escape the repository root when
// passed as a git pathspec.
func isSafePath(path string) bool {
	if path == "" {
		return false
	}
	// filepath.IsLocal rejects absolute paths, ".." traversal and
	// drive/UNC roots. Git paths from porcelain are slash-separated,
	// so convert before validating on Windows.
	return filepath.IsLocal(filepath.FromSlash(path))
}
