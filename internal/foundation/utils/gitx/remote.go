package gitx

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// RemoteInfo is the parsed repository remote that the Git panel's PR
// view needs. Only the origin remote is used: a fork checked out as
// origin reports the fork's owner/repo, matching what GitHub lists for
// that repository.
type RemoteInfo struct {
	// Host is the remote hostname without a port (github.com for
	// GitHub remotes).
	Host string
	// Owner and Repo are the URL path segments of the remote.
	Owner string
	Repo  string
}

// nameRe is deliberately conservative: GitHub owner/repo names only
// contain ASCII letters, digits, dots, dashes and underscores. Anything
// else is treated as not being a parseable GitHub-style remote so the
// caller never interpolates unsanitized segments into an API path.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Remote resolves and parses the origin remote URL of root. ok is false
// when the repository has no origin remote or the URL is not a
// host/owner/repo form git understands.
func Remote(ctx context.Context, root string) (RemoteInfo, bool) {
	if root == "" {
		return RemoteInfo{}, false
	}
	raw, _ := RunBounded(ctx, root, 8<<10, 5*time.Second,
		"remote", "get-url", "origin")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RemoteInfo{}, false
	}
	return parseRemote(raw)
}

// parseRemote parses the remote URL forms git accepts:
//
//   - https://github.com/owner/repo.git
//   - git@github.com:owner/repo.git
//   - ssh://git@github.com/owner/repo
//   - https://user@github.com/owner/repo
//
// Host:port and trailing slashes are tolerated; the optional ".git"
// suffix is stripped.
func parseRemote(raw string) (RemoteInfo, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RemoteInfo{}, false
	}
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Path == "" {
			return RemoteInfo{}, false
		}
		host = u.Hostname()
		path = strings.Trim(u.Path, "/")
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"):
		// scp-like syntax: [user@]host:owner/repo[.git]
		at := strings.LastIndex(raw, "@")
		colon := strings.Index(raw[at+1:], ":")
		if colon < 0 {
			return RemoteInfo{}, false
		}
		host = raw[at+1 : at+1+colon]
		path = strings.Trim(raw[at+2+colon:], "/")
	default:
		return RemoteInfo{}, false
	}
	segments := strings.Split(path, "/")
	if host == "" || len(segments) != 2 {
		return RemoteInfo{}, false
	}
	repo := strings.TrimSuffix(segments[1], ".git")
	if !nameRe.MatchString(host) || !nameRe.MatchString(segments[0]) ||
		!nameRe.MatchString(repo) {
		return RemoteInfo{}, false
	}
	return RemoteInfo{
		Host:  host,
		Owner: segments[0],
		Repo:  repo,
	}, true
}
