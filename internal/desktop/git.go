package desktop

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/undo"
)

const (
	gitSnapshotTimeout  = 10 * time.Second
	maxGitSnapshotBytes = 4 << 20 // 4 MiB of porcelain status
	maxGitSnapshotPaths = 2000    // cap the per-turn undo snapshot
)

// gitChangedPaths returns workspace-relative paths that git reports as
// changed, staged, or untracked. It returns nil when the workspace is
// not a git repository or git is unavailable.
func gitChangedPaths(ctx context.Context, wd string) []string {
	repo := gitRepoRoot(wd)
	if repo == "" {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, gitSnapshotTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx,
		"git", "-C", repo,
		"status", "--porcelain", "--untracked-files=all", "-z")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	out, err := io.ReadAll(io.LimitReader(stdout, maxGitSnapshotBytes+1))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil
	}
	if len(out) > maxGitSnapshotBytes {
		// Too many changes to snapshot safely: skip undo for this turn
		// rather than buffering an unbounded status.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil
	}
	if err := cmd.Wait(); err != nil {
		return nil
	}
	parts := strings.Split(string(out), "\x00")
	var paths []string
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 3 {
			continue
		}
		code := rec[:2]
		path := strings.TrimPrefix(rec[2:], " ")
		// Rename/copy entries carry the destination as the next field.
		if (code[0] == 'R' || code[0] == 'C') && i+1 < len(parts) &&
			parts[i+1] != "" {
			i++
			path = parts[i]
		}
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
			if len(paths) >= maxGitSnapshotPaths {
				return nil
			}
		}
	}
	return paths
}

// gitSnapshot reads the current content of every changed/untracked
// file into undo states. Files over undo.MaxFileBytes are marked
// skipped so undo/redo leave them untouched.
func gitSnapshot(ctx context.Context, wd string) []undo.FileState {
	paths := gitChangedPaths(ctx, wd)
	if len(paths) == 0 {
		return nil
	}
	states := make([]undo.FileState, 0, len(paths))
	for _, rel := range paths {
		abs := filepath.Join(wd, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		st := undo.FileState{Path: rel}
		data, err := os.ReadFile(abs)
		if err != nil {
			st.Present = false
		} else {
			st.Present = true
			if len(data) > undo.MaxFileBytes {
				st.Skipped = true
			} else {
				st.Content = string(data)
			}
		}
		states = append(states, st)
	}
	return states
}

// gitRepoRoot walks upward from dir looking for a .git marker. Empty
// means the workspace is not inside a git repository.
func gitRepoRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
