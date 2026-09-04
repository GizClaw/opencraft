// Package gitx centralizes bounded, read-only git access shared by
// worldstate context snapshots and desktop artifact manifest snapshots.
// It never mutates the repository.
package gitx

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

// Root walks upward from dir looking for a .git marker. Empty means
// dir is not inside a git repository.
func Root(dir string) string {
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

// RunBounded runs one git command and reads at most limit bytes of
// stdout. Oversized output is killed instead of buffered. The second
// return value reports whether output was truncated.
func RunBounded(
	ctx context.Context,
	root string,
	limit int64,
	timeout time.Duration,
	args ...string,
) (string, bool) {
	if root == "" || limit <= 0 {
		return "", false
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
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

// ChangedOptions bounds the porcelain status snapshot.
type ChangedOptions struct {
	// MaxBytes caps the raw porcelain output; oversized repos are
	// reported as nil so callers skip rather than buffer unbounded
	// state.
	MaxBytes int64
	// MaxPaths caps the number of returned paths; nil is returned when
	// a repository has more changes than this.
	MaxPaths int
}

// ChangedPaths returns workspace-relative, slash-separated paths that
// git reports as changed, staged, copied, renamed or untracked. It
// returns nil when git is unavailable, output is over budget, or there
// are more paths than MaxPaths.
func ChangedPaths(
	ctx context.Context,
	root string,
	opts ChangedOptions,
) []string {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 4 << 20
	}
	if opts.MaxPaths <= 0 {
		opts.MaxPaths = 2000
	}
	out, truncated := RunBounded(ctx, root, opts.MaxBytes, 10*time.Second,
		"status", "--porcelain", "--untracked-files=all", "-z")
	if truncated {
		return nil
	}
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 3 {
			continue
		}
		code := rec[:2]
		path := strings.TrimPrefix(rec[2:], " ")
		if (code[0] == 'R' || code[0] == 'C') && i+1 < len(parts) &&
			parts[i+1] != "" {
			i++
			path = parts[i]
		}
		if path == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(path))
		if len(paths) >= opts.MaxPaths {
			return nil
		}
	}
	return paths
}

// CapLines keeps the first max lines and appends a "+N more" marker.
func CapLines(s string, max int) string {
	if max <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n") +
		fmt.Sprintf("\n…(+%d more)", len(lines)-max)
}
