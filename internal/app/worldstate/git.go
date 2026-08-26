package worldstate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	gitStatusBudget   = 4 << 10 // 4 KiB of short status
	gitDiffStatBudget = 2 << 10 // 2 KiB of diffstat
	gitDiffBudget     = 6 << 10 // 6 KiB of unified diff
	gitStatusMaxLines = 100     // untracked-file flood guard
	gitTimeout        = 5 * time.Second
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

	git := func(args ...string) string {
		cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", root}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(out), "\n")
	}

	branch := git("branch", "--show-current")
	status := git("status", "--short", "--untracked-files=all")
	diffstat := git("diff", "--stat", "--no-color")
	if cached := git("diff", "--cached", "--stat", "--no-color"); cached != "" {
		if diffstat != "" {
			diffstat += "\n"
		}
		diffstat += cached
	}
	diff := git("diff", "HEAD", "--no-color")
	diffHint := ""
	if len(diff) > gitDiffBudget {
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

// capStatusLines keeps the first max lines of a status listing and
// appends a "+N more" marker, so a flood of untracked files cannot
// dominate the context.
func capStatusLines(s string, max int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n") +
		fmt.Sprintf("\n…(+%d more)", len(lines)-max)
}
