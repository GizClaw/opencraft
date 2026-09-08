// Package repo executes bounded git write operations for the desktop
// Git panel. Unlike foundation/utils/gitx (read-only by contract),
// these operations mutate the user's repository, so they are kept
// behind one service that serializes per repository, validates every
// path, disables interactive prompts and records a best-effort audit
// trail. Behavior intentionally matches the system git CLI so hooks,
// credential helpers, worktrees and user configuration behave exactly
// as they do for the agent's exec tools.
package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
)

// Kind identifies one git mutation.
type Kind string

const (
	KindStage           Kind = "stage"
	KindUnstage         Kind = "unstage"
	KindCommit          Kind = "commit"
	KindCheckout        Kind = "checkout"
	KindNewBranch       Kind = "new_branch"
	KindDiscardWorktree Kind = "discard_worktree"
	KindDiscardBoth     Kind = "discard_both"
	KindClean           Kind = "clean"
	KindPull            Kind = "pull"
	KindPush            Kind = "push"
	KindForcePush       Kind = "force_push"
)

// Op is one write request. Paths are repo-relative slash paths; they
// are validated before reaching git.
type Op struct {
	Kind    Kind
	Paths   []string
	Message string
	Branch  string
}

// Request carries the repository context for one op.
type Request struct {
	// Root is the repository top level resolved by git.
	Root string
	// AuditDir receives git-ops.jsonl records when non-empty. Audit
	// writes are best-effort and never block the operation.
	AuditDir string
	Op       Op
	// Timeout overrides the per-kind default when positive.
	Timeout time.Duration
	// MaxOutput caps combined stdout/stderr capture.
	MaxOutput int64
}

// Result carries the command's captured output.
type Result struct {
	Output    string
	Truncated bool
}

// branchNameRe is the base charset gate for branch names: option
// injection (a leading "-"), spaces, control bytes and shell
// metacharacters are rejected before git sees them. Slashes allow
// hierarchical names; validBranchName adds git's structural rules on
// top.
var branchNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// validBranchName applies git check-ref-format's structural rules on
// top of branchNameRe: no empty segments ("//"), no ".." anywhere, no
// "@{" (revision magic), no trailing slash or dot, no ".lock" suffix,
// and no component that starts with "." or ends with ".lock". Git is
// still the final authority when a name slips past these mirrors, but
// `git switch` never falls back to a pathspec, so a file whose name
// also looks like a branch can never be checked out silently.
func validBranchName(name string) bool {
	if !branchNameRe.MatchString(name) {
		return false
	}
	if strings.Contains(name, "..") ||
		strings.Contains(name, "@{") ||
		strings.Contains(name, "//") ||
		strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".") ||
		strings.HasSuffix(name, ".lock") {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") ||
			strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
}

var (
	lockMu sync.Mutex
	locks  = map[string]*sync.Mutex{}
)

// Run executes one serialized, bounded git mutation.
func Run(ctx context.Context, req Request) (Result, error) {
	if req.Root == "" {
		return Result{}, errors.New("repo: repository root is required")
	}
	if req.MaxOutput <= 0 {
		req.MaxOutput = 2 << 20
	}
	args, err := buildArgs(req.Op)
	if err != nil {
		return Result{}, err
	}
	if req.Op.Kind != KindPull && req.Op.Kind != KindPush &&
		req.Op.Kind != KindForcePush {
		if gitx.HasUnmerged(ctx, req.Root) {
			return Result{}, errors.New(
				"repo: unmerged paths exist; resolve conflicts before this operation")
		}
	}
	lock := lockFor(req.Root)
	lock.Lock()
	defer lock.Unlock()

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
		switch req.Op.Kind {
		case KindPull, KindPush, KindForcePush:
			timeout = 3 * time.Minute
		}
	}
	res := runBounded(ctx, req.Root, args, req.MaxOutput, timeout)
	ok := res.err == nil
	if req.AuditDir != "" {
		recordAudit(req.AuditDir, auditEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			RepoRoot:  req.Root,
			Kind:      string(req.Op.Kind),
			Paths:     req.Op.Paths,
			Branch:    req.Op.Branch,
			OK:        ok,
			Error:     errText(res.err),
		})
	}
	if res.err != nil {
		detail := res.output
		if detail == "" {
			detail = res.err.Error()
		}
		return Result{Output: res.output, Truncated: res.truncated},
			fmt.Errorf("repo: git %s failed: %s",
				req.Op.Kind, capTail(detail))
	}
	return Result{Output: res.output, Truncated: res.truncated}, nil
}

func buildArgs(op Op) ([]string, error) {
	switch op.Kind {
	case KindStage:
		paths, err := checkedPaths(op.Paths)
		if err != nil {
			return nil, err
		}
		return append([]string{"add", "-A", "--"}, paths...), nil
	case KindUnstage:
		paths, err := checkedPaths(op.Paths)
		if err != nil {
			return nil, err
		}
		return append([]string{"restore", "--staged", "--"}, paths...), nil
	case KindCommit:
		if strings.TrimSpace(op.Message) == "" {
			return nil, errors.New("repo: commit message is required")
		}
		if len(op.Message) > 4096 || strings.ContainsRune(op.Message, '\x00') {
			return nil, errors.New("repo: commit message is invalid")
		}
		return []string{"commit", "-m", op.Message}, nil
	case KindCheckout:
		if !validBranchName(op.Branch) {
			return nil, errors.New("repo: invalid branch name")
		}
		return []string{"switch", op.Branch}, nil
	case KindNewBranch:
		if !validBranchName(op.Branch) {
			return nil, errors.New("repo: invalid branch name")
		}
		return []string{"switch", "-c", op.Branch}, nil
	case KindDiscardWorktree:
		paths, err := checkedPaths(op.Paths)
		if err != nil {
			return nil, err
		}
		return append([]string{"restore", "--worktree", "--"}, paths...), nil
	case KindDiscardBoth:
		paths, err := checkedPaths(op.Paths)
		if err != nil {
			return nil, err
		}
		return append(
			[]string{"restore", "--staged", "--worktree", "--"}, paths...), nil
	case KindClean:
		paths, err := checkedPaths(op.Paths)
		if err != nil {
			return nil, err
		}
		return append([]string{"clean", "-fd", "--"}, paths...), nil
	case KindPull:
		return []string{"pull"}, nil
	case KindPush:
		return []string{"push"}, nil
	case KindForcePush:
		return []string{"push", "--force-with-lease"}, nil
	default:
		return nil, fmt.Errorf("repo: unsupported operation %q", op.Kind)
	}
}

func checkedPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("repo: at least one path is required")
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || !filepath.IsLocal(filepath.FromSlash(p)) {
			return nil, fmt.Errorf("repo: invalid path %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func lockFor(root string) *sync.Mutex {
	lockMu.Lock()
	defer lockMu.Unlock()
	key := filepath.Clean(root)
	mu := locks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		locks[key] = mu
	}
	return mu
}

type boundedResult struct {
	output    string
	truncated bool
	err       error
}

// runBounded starts one git command with prompts disabled and captures
// at most maxOutput bytes of combined output.
func runBounded(
	ctx context.Context,
	root string,
	args []string,
	maxOutput int64,
	timeout time.Duration,
) boundedResult {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(
		runCtx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr cappedBuffer
	stdout.max = maxOutput / 2
	stderr.max = maxOutput / 2
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return boundedResult{err: err}
	}
	waitErr := cmd.Wait()
	out := strings.TrimSpace(stdout.String() + stderr.String())
	if stdout.overflow || stderr.overflow {
		out = capTail(out)
		return boundedResult{
			output:    out,
			truncated: true,
			err:       waitErr,
		}
	}
	return boundedResult{output: out, err: waitErr}
}

// cappedBuffer is an io.Writer that stops accepting data after max
// bytes. exec.Cmd writes each stream from its own goroutine, so two
// buffers never contend.
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int64
	overflow bool
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if w.max <= 0 {
		w.max = 1 << 20
	}
	room := int(w.max) - w.buf.Len()
	if room < 0 {
		room = 0
	}
	if len(p) > room {
		w.buf.Write(p[:room])
		w.overflow = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *cappedBuffer) String() string { return w.buf.String() }

func capTail(s string) string {
	if len(s) <= 8192 {
		return s
	}
	return "…" + s[len(s)-8192:]
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type auditEntry struct {
	Timestamp string   `json:"timestamp"`
	RepoRoot  string   `json:"repo_root"`
	Kind      string   `json:"kind"`
	Paths     []string `json:"paths,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
}

func recordAudit(dir string, entry auditEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"repo: marshal audit record failed", err)
		return
	}
	path := filepath.Join(dir, "git-ops.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"repo: open audit file failed", err)
		return
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		telemetry.WarnErr(context.Background(),
			"repo: append audit record failed", err)
	}
	if err := f.Close(); err != nil {
		telemetry.WarnErr(context.Background(),
			"repo: close audit file failed", err)
	}
}
