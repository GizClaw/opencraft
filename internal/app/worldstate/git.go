package worldstate

import (
	"context"
	"fmt"
	"io"
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

// gitBounded runs one git command and reads at most limit bytes of
// stdout. Oversized output is killed instead of buffered, so a huge
// repo cannot exhaust memory during the per-turn worldstate snapshot.
// The second return value reports whether the output was truncated.
func gitBounded(
	ctx context.Context,
	root string,
	limit int64,
	args ...string,
) (string, bool) {
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", root}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	if err := cmd.Start(); err != nil {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(stdout, limit+1))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", false
	}
	truncated := int64(len(data)) > limit
	if truncated {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", true
	}
	if err := cmd.Wait(); err != nil {
		return "", false
	}
	return strings.TrimRight(string(data), "\n"), false
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
