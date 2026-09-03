package worldstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
)

const (
	gitStatusBudget   = 4 << 10 // 4 KiB of short status
	gitDiffStatBudget = 2 << 10 // 2 KiB of diffstat
	gitDiffBudget     = 6 << 10 // 6 KiB of unified diff
	gitStatusMaxLines = 100     // untracked-file flood guard
	gitTimeout        = 5 * time.Second
	// Read caps for the raw git output. The in-context budgets above
	// truncate after capture; these caps keep the capture itself from
	// buffering a pathological diff/status (e.g. a huge repo) into
	// memory.
	gitStatusReadCap   = 64 << 10
	gitDiffStatReadCap = 16 << 10
	gitDiffReadCap     = 64 << 10
)

// gitSection renders a bounded snapshot of repository state: current
// branch, short status, diffstat, and a bounded unified diff of all
// changes (staged + unstaged). Workspaces outside a git repository
// yield an empty section, so non-git projects are unaffected.
func (s *Service) gitSection(ctx context.Context) Section {
	root := projectRoot(s.opts.WorkBase)
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return Section{}
	}
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	branch, _ := gitBounded(runCtx, root, gitStatusReadCap,
		"branch", "--show-current")
	status, _ := gitBounded(runCtx, root, gitStatusReadCap,
		"status", "--short", "--untracked-files=all")
	diffstat, _ := gitBounded(runCtx, root, gitDiffStatReadCap,
		"diff", "--stat", "--no-color")
	if cached, _ := gitBounded(runCtx, root, gitDiffStatReadCap,
		"diff", "--cached", "--stat", "--no-color"); cached != "" {
		if diffstat != "" {
			diffstat += "\n"
		}
		diffstat += cached
	}
	diff, truncated := gitBounded(runCtx, root, gitDiffReadCap,
		"diff", "HEAD", "--no-color")
	diffHint := ""
	if truncated || len(diff) > gitDiffBudget {
		// A mid-cut diff is worse than no diff: omit it and let the
		// model pull the exact hunks it needs with git diff / read_file.
		diffHint = "diff omitted (too large); run `git diff HEAD -- <path>` or read_file for details"
		diff = ""
	}

	if branch == "" && status == "" && diffstat == "" &&
		diff == "" && diffHint == "" {
		return Section{}
	}

	out, err := render(gitTmpl, gitData{
		Branch:   branch,
		Status:   capStatusLines(truncateBytes(status, gitStatusBudget), gitStatusMaxLines),
		DiffStat: truncateBytes(diffstat, gitDiffStatBudget),
		Diff:     truncateBytes(diff, gitDiffBudget),
		DiffHint: diffHint,
	})
	if err != nil {
		return Section{}
	}
	return Section{ID: "git", Role: "system", Text: strings.TrimRight(out, "\n")}
}

// gitBounded is a thin wrapper over gitx.RunBounded kept for local
// call sites and tests.
func gitBounded(
	ctx context.Context,
	root string,
	limit int64,
	args ...string,
) (string, bool) {
	return gitBoundedWithTimeout(ctx, root, limit, gitTimeout, args...)
}

// gitBoundedWithTimeout delegates to gitx.RunBounded with an explicit
// timeout.
func gitBoundedWithTimeout(
	ctx context.Context,
	root string,
	limit int64,
	timeout time.Duration,
	args ...string,
) (string, bool) {
	return gitx.RunBounded(ctx, root, limit, timeout, args...)
}

// capStatusLines is a thin wrapper over gitx.CapLines.
func capStatusLines(s string, max int) string {
	return gitx.CapLines(s, max)
}
