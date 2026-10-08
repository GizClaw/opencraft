// Install sources: where a package comes from.
//
// One string names every source this host accepts, and one resolution
// turns it into the directory the rest of the pipeline reads. Inspect,
// Install and Update all start here, so "src" means one thing in all
// three: the directory a user picked, the archive they downloaded, the
// git remote they pasted, the URL a README published.
//
// The vocabulary is by prefix, and a string that is none of them is
// refused with the ones that are:
//
//	/path/to/package          a directory on this machine
//	/path/to/package.zip      an archive (any file: the layout rules are
//	                          app.yaml at the root or under a single
//	                          top-level directory)
//	git+https://host/o/r.git#main
//	                          a git remote, cloned shallow at the ref
//	                          (a branch or tag; the tree is the package,
//	                          the repository is only where it came from)
//	git+file:///path/to/repo#main
//	                          the same, from a repository on this machine
//	https://host/path/pkg.zip
//	                          an archive URL, downloaded and unpacked
//
// Guessing is what this shape exists to avoid: "github.com/owner/repo"
// could be either half of the vocabulary, and a host that guessed would
// fetch something the user did not name. So each remote spelling is
// explicit, and the refusals teach the one that would have worked.
package apps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/utils/netguard"
	"github.com/GizClaw/opencraft/internal/foundation/utils/zipx"
)

const (
	// gitPrefix marks a git remote in the source vocabulary.
	gitPrefix = "git+"
	// defaultSourceBytes bounds one downloaded archive. zipx checks the
	// archive's own declared sizes on top of this; this is the bound
	// that stops a server from streaming forever.
	defaultSourceBytes int64 = 256 << 20
	// cloneTimeout bounds one clone. A package clone is shallow and
	// single-branch, so this is generous; what it protects against is a
	// remote that accepts the connection and then says nothing.
	cloneTimeout = 2 * time.Minute
	// cloneOutputBytes bounds what a failed clone's output can add to
	// an error message.
	cloneOutputBytes = 8 << 10
)

// sourceKind is how a source was resolved, for the places that want to
// say so (and for tests that pin which half of the vocabulary ran).
type sourceKind string

const (
	sourceDir sourceKind = "dir"
	sourceZip sourceKind = "zip"
	sourceGit sourceKind = "git"
	sourceURL sourceKind = "url"
)

// source is one resolved install source: a directory holding the
// package, plus the cleanup that removes whatever staging the resolution
// needed (an extraction root, a clone, a downloaded archive).
type source struct {
	kind sourceKind
	// dir is the package tree every caller reads.
	dir string
	// remote and ref are the git source, for error text and telemetry.
	remote string
	ref    string
	// cleanup removes the staging. It is never nil, so callers defer it
	// without a nil check.
	cleanup func()
}

// readsAsHost reports whether a source string that is not on disk reads
// like a host name rather than a path: its first segment carries a dot
// ("github.com/owner/repo"), which is the paste that would otherwise be
// answered with a bare file-not-found.
func readsAsHost(path string) bool {
	first := path
	if i := strings.IndexAny(first, `/\`); i >= 0 {
		first = first[:i]
	}
	return first != "." && first != ".." && strings.Contains(first, ".")
}

// resolveSource turns one source string into a package directory. The
// caller owns the returned cleanup.
func (s *Store) resolveSource(ctx context.Context, raw string) (source, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return source{}, errors.New("apps: source is required")
	}
	if remote, ok := strings.CutPrefix(trimmed, gitPrefix); ok {
		return s.resolveGit(ctx, trimmed, remote)
	}
	if u, err := url.Parse(trimmed); err == nil && looksLikeURL(u) {
		return s.resolveArchiveURL(ctx, trimmed, u)
	}
	return s.resolveLocal(trimmed)
}

// resolveLocal reads a path on this machine: a directory, or a file that
// is an archive. The registry's own content root is refused as a source
// either way — an install from there would copy the registry into itself.
func (s *Store) resolveLocal(path string) (source, error) {
	info, err := os.Stat(path)
	if err != nil {
		// The near miss this shape invites: a host name pasted without
		// its scheme ("github.com/owner/repo"), which reads as a
		// relative path and dies on "no such file". Say what a remote
		// source looks like instead of leaving it at the stat error.
		if readsAsHost(path) {
			return source{}, fmt.Errorf(
				"apps: source %q: %w; a remote source is written %shttps://host/owner/repo.git or https://host/package.zip",
				path, err, gitPrefix)
		}
		return source{}, fmt.Errorf("apps: source: %w", err)
	}
	if err := s.checkOutsideRoot(path); err != nil {
		return source{}, err
	}
	if info.IsDir() {
		return source{kind: sourceDir, dir: path, cleanup: func() {}}, nil
	}
	dir, cleanup, err := zipx.Extract(path, ManifestFile)
	if err != nil {
		return source{}, err
	}
	return source{kind: sourceZip, dir: dir, cleanup: cleanup}, nil
}

// resolveGit clones a git remote into a staging directory. The clone is
// shallow and single-branch: a package is a tree at one version, and the
// history that produced it is not part of the installation.
func (s *Store) resolveGit(
	ctx context.Context,
	display string,
	remote string,
) (source, error) {
	urlPart, ref := splitRef(remote)
	if urlPart == "" {
		return source{}, fmt.Errorf("apps: %q names no repository", display)
	}
	u, err := url.Parse(urlPart)
	if err != nil {
		return source{}, fmt.Errorf("apps: git source %q: %w", display, err)
	}
	switch u.Scheme {
	case "https":
		// The guards the host fetches any URL under (netguard): the
		// source arrives from a paste or a README, not from a tool call
		// the user aimed.
		if err := netguard.CheckURL(ctx, u, s.sourcePolicy); err != nil {
			return source{}, fmt.Errorf("apps: git source %q: %w", display, err)
		}
	case "file":
		// A repository on this machine: there is no network to guard,
		// and the user already has the files.
		if u.Host != "" && u.Host != "localhost" {
			return source{}, fmt.Errorf(
				"apps: git source %q: file:// names a repository on this machine",
				display)
		}
	case "ssh", "git":
		return source{}, fmt.Errorf(
			"apps: git source %q: the host does not run ssh; write it git+https://…, or clone it yourself and install the directory",
			display)
	default:
		return source{}, fmt.Errorf(
			"apps: git source %q: unsupported scheme %q (want git+https://… or git+file:///…)",
			display, u.Scheme)
	}
	dir, cleanup, err := gitClone(ctx, urlPart, ref)
	if err != nil {
		return source{}, err
	}
	return source{
		kind:    sourceGit,
		dir:     dir,
		remote:  urlPart,
		ref:     ref,
		cleanup: cleanup,
	}, nil
}

// resolveArchiveURL downloads an archive and unpacks it under the same
// layout rules a local archive gets.
func (s *Store) resolveArchiveURL(
	ctx context.Context,
	display string,
	u *url.URL,
) (source, error) {
	// A git remote written without the prefix is the most common thing to
	// paste here, and "not a zip archive" would be a bad way to say so.
	if strings.HasSuffix(strings.ToLower(u.Path), ".git") {
		return source{}, fmt.Errorf(
			"apps: %q is a git remote: write it %s%s to clone it, or download the archive and install the file",
			display, gitPrefix, display)
	}
	if err := netguard.CheckURL(ctx, u, s.sourcePolicy); err != nil {
		return source{}, fmt.Errorf("apps: source %q: %w", display, err)
	}
	archive, cleanupArchive, err := s.downloadArchive(ctx, u)
	if err != nil {
		return source{}, err
	}
	dir, cleanupExtract, err := zipx.Extract(archive, ManifestFile)
	if err != nil {
		cleanupArchive()
		return source{}, err
	}
	cleanup := func() {
		cleanupExtract()
		cleanupArchive()
	}
	return source{kind: sourceURL, dir: dir, cleanup: cleanup}, nil
}

// downloadArchive streams one archive to a temporary file. The bound is
// on the bytes actually written — a Content-Length is a claim, not a
// measurement — and the file is removed again by the returned cleanup.
func (s *Store) downloadArchive(
	ctx context.Context,
	u *url.URL,
) (string, func(), error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("apps: download request: %w", err)
	}
	resp, err := netguard.Client(s.sourcePolicy).Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("apps: download %s: %w", u.Redacted(), err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "apps: close source response failed",
			resp.Body.Close())
	}()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf(
			"apps: download %s returned %s", u.Redacted(), resp.Status)
	}
	tmp, err := os.CreateTemp("", "oc-app-source-*.zip")
	if err != nil {
		return "", nil, fmt.Errorf("apps: temp file: %w", err)
	}
	cleanup := func() {
		telemetry.WarnErr(ctx, "apps: close source temp failed", tmp.Close())
		telemetry.WarnErr(ctx, "apps: remove source temp failed",
			os.Remove(tmp.Name()))
	}
	// The bound is read from the Store rather than the constant so it
	// can be exercised: it is the one rule here that a test cannot reach
	// with the shipped 256 MiB (see Store.sourceBytes).
	limit := s.sourceByteLimit()
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("apps: download %s: %w", u.Redacted(), err)
	}
	if written > limit {
		cleanup()
		return "", nil, fmt.Errorf(
			"apps: download %s is larger than the %.1f MiB one package may be",
			u.Redacted(), float64(limit)/(1<<20))
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("apps: close temp file: %w", err)
	}
	return tmp.Name(), cleanup, nil
}

// splitRef separates the ref a git source names from the remote. The ref
// is a branch or a tag (a shallow single-branch clone is a clone of one
// of those); without one the remote's default branch is what arrives.
func splitRef(remote string) (string, string) {
	if i := strings.LastIndex(remote, "#"); i >= 0 {
		return remote[:i], strings.TrimSpace(remote[i+1:])
	}
	return remote, ""
}

// gitClone clones one remote into a temporary directory and returns that
// directory, minus its .git. What the staging directory is handed to —
// the preflight, the install copy, the landing-path walk — all mirror
// copyTree, which skips dot entries, so a repository left in place is
// not read today; but "the copy happens to filter it" is a thin answer
// for the one directory the host holds out as the package, and a
// repository's object database has no business in an application's
// content root.
func gitClone(ctx context.Context, remote, ref string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "oc-app-source-*")
	if err != nil {
		return "", nil, fmt.Errorf("apps: temp dir: %w", err)
	}
	cleanup := func() {
		telemetry.WarnErr(ctx, "apps: remove source clone failed", os.RemoveAll(dir))
	}
	runCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()
	args := []string{"clone", "--depth", "1", "--single-branch", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	// -- ends the options: a remote string is user input, and git would
	// otherwise read "-oProxyCommand=…" as a clone option.
	args = append(args, "--", remote, dir)
	cmd := exec.CommandContext(runCtx, "git", args...)
	// No prompt. A credential prompt in a child with no terminal hangs
	// until the timeout, and whatever the user typed would never be shown
	// to them.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		cleanup()
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return "", nil, errors.New(
				"apps: installing from a git remote needs git on PATH")
		case runCtx.Err() != nil:
			return "", nil, fmt.Errorf(
				"apps: git clone %s timed out after %s", remote, cloneTimeout)
		}
		return "", nil, fmt.Errorf("apps: git clone %s: %w%s",
			remote, err, out.suffix())
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("apps: drop the clone's repository: %w", err)
	}
	return dir, cleanup, nil
}

// cappedBuffer collects the first cloneOutputBytes written to it and
// remembers that more arrived, so a noisy git cannot grow the host's
// memory through an error message.
type cappedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := cloneOutputBytes - c.buf.Len()
	switch {
	case room <= 0:
		c.truncated = true
	case len(p) > room:
		c.buf.Write(p[:room])
		c.truncated = true
	default:
		c.buf.Write(p)
	}
	return len(p), nil
}

// suffix renders the collected output as the tail of an error message:
// git states the reason last, and the indentation keeps it readable next
// to the sentence that explains what was attempted.
func (c *cappedBuffer) suffix() string {
	text := strings.TrimSpace(c.buf.String())
	if text == "" {
		return ""
	}
	if c.truncated {
		text += "\n(output truncated)"
	}
	return ": " + strings.ReplaceAll(text, "\n", "\n    ")
}

// looksLikeURL reports whether a source string is a URL rather than a
// path. A Windows drive letter ("C:\packages\app") parses as a
// one-letter scheme, which is the case this check is about.
func looksLikeURL(u *url.URL) bool {
	if u.Scheme == "" {
		return false
	}
	return len(u.Scheme) > 1 || strings.HasPrefix(u.Opaque, "//")
}
