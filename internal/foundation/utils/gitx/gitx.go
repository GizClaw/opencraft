// Package gitx centralizes bounded, read-only git access shared by
// worldstate context snapshots and desktop artifact manifest snapshots.
// It never mutates the repository.
package gitx

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
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
	attrs := []otellog.KeyValue{
		otellog.String("git.root", root),
		otellog.String("git.args", strings.Join(args, " ")),
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		telemetry.WarnErr(ctx, "gitx: create stdout pipe failed", err, attrs...)
		return "", false
	}
	if err := cmd.Start(); err != nil {
		telemetry.WarnErr(ctx, "gitx: start git failed", err, attrs...)
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(stdout, limit+1))
	if err != nil {
		telemetry.WarnErr(ctx, "gitx: read git output failed", err, attrs...)
		telemetry.WarnErr(ctx, "gitx: kill git after read failure",
			cmd.Process.Kill())
		telemetry.WarnErr(ctx, "gitx: wait git after read failure", cmd.Wait())
		return "", false
	}
	truncated := int64(len(data)) > limit
	if truncated {
		telemetry.WarnErr(ctx, "gitx: kill truncated git output",
			cmd.Process.Kill())
		telemetry.WarnErr(ctx, "gitx: wait truncated git output", cmd.Wait())
		return "", true
	}
	if err := cmd.Wait(); err != nil {
		telemetry.WarnErr(ctx, "gitx: git command failed", err, attrs...)
		return "", false
	}
	return strings.TrimRight(string(data), "\n"), false
}
