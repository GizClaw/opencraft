package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// writeFakeGH writes a shell script that stands in for the gh CLI and
// returns its path as the binary lookup result.
func writeFakeGH(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a shell script")
	}
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestGHAuthTokenIgnoresStderr pins where the credential comes from:
// stdout alone. gh prints warnings and progress notes on stderr (a
// GH_TOKEN it refuses to use is the common one), and a shared output
// buffer spliced them into the token, producing an Authorization header
// that authenticates as nothing.
func TestGHAuthTokenIgnoresStderr(t *testing.T) {
	bin := writeFakeGH(t,
		"echo 'warning: GH_TOKEN in the environment is being ignored' >&2\n"+
			"echo ghp_realtoken\n")
	token, err := ghAuthToken(context.Background(), lookupOK(bin))
	if err != nil {
		t.Fatalf("ghAuthToken: %v", err)
	}
	if token != "ghp_realtoken" {
		t.Fatalf("token = %q, want the stdout token alone", token)
	}
}

// TestGHAuthTokenRefusesNonCredentialOutput: a token is one
// whitespace-free word, so anything else is not one.
func TestGHAuthTokenRefusesNonCredentialOutput(t *testing.T) {
	bin := writeFakeGH(t, "echo 'no token available right now'\n")
	if _, err := ghAuthToken(context.Background(), lookupOK(bin)); !errors.Is(
		err, ErrNoProvider) {
		t.Fatalf("err = %v, want ErrNoProvider", err)
	}
}

// TestCommitsDoNotFollowForeignLink pins the pagination boundary: the
// Link header is followed only when it stays on the configured API
// origin, because the follow-up request carries the provider token. A
// second server stands in for wherever a hostile or misconfigured
// response tried to send it, and must receive nothing.
func TestCommitsDoNotFollowForeignLink(t *testing.T) {
	var foreignHits atomic.Int32
	var foreign *httptest.Server
	foreign = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			foreignHits.Add(1)
			writeJSON(t, w, commitRows(3))
		}))
	defer foreign.Close()

	home := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// A next page, but on another origin.
			w.Header().Set("Link", fmt.Sprintf(
				`<%s/repos/o/r/pulls/1/commits?page=2>; rel="next"`,
				foreign.URL))
			writeJSON(t, w, commitRows(3))
		}))
	defer home.Close()

	c := &apiClient{base: home.URL, http: home.Client(), token: "tok"}
	commits, err := c.commits(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("commits: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %d, want the first page only", len(commits))
	}
	if got := foreignHits.Load(); got != 0 {
		t.Fatalf("the foreign origin received %d requests", got)
	}
}

// TestNextLinkKeepsTheAPIOrigin is the unit half of the rule: a link on
// the same origin survives (scheme and host compared case-insensitively,
// as URL schemes and hosts are), and everything else is dropped.
func TestNextLinkKeepsTheAPIOrigin(t *testing.T) {
	c := &apiClient{base: "https://api.github.com"}
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"same origin", `<https://api.github.com/repos/o/r/pulls?page=2>; rel="next"`,
			"https://api.github.com/repos/o/r/pulls?page=2"},
		{"same origin, upper case host",
			`<https://API.GitHub.COM/repos/o/r/pulls?page=2>; rel="next"`,
			"https://API.GitHub.COM/repos/o/r/pulls?page=2"},
		{"another host", `<https://evil.example/repos/o/r/pulls?page=2>; rel="next"`, ""},
		{"another scheme", `<http://api.github.com/repos/o/r/pulls?page=2>; rel="next"`, ""},
		{"relative", `</repos/o/r/pulls?page=2>; rel="next"`, ""},
		{"last page", `<https://api.github.com/repos/o/r/pulls?page=9>; rel="last"`, ""},
		{"garbage", `<<<>>; rel="next"`, ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.nextLink(tc.header); got != tc.want {
				t.Errorf("nextLink(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}
