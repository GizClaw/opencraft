package bindings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	gitxgh "github.com/GizClaw/opencraft/internal/foundation/utils/gitx/gh"
)

func newPRBinding(
	t *testing.T,
	remoteURL string,
	baseURL string,
) *PullRequests {
	t.Helper()
	root := t.TempDir()
	initRepoInTest(t, root)
	if remoteURL != "" {
		gitInTest(t, root, "remote", "add", "origin", remoteURL)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), root)
	c.Shell.SetContext(context.Background())
	svc := gitxgh.NewService(gitxgh.Options{
		BaseURL: baseURL,
		TokenFn: func(context.Context) (string, error) {
			return "tok-test", nil
		},
	})
	c.Git = core.NewGitServiceWithRemote(svc)
	return NewPullRequestsBinding(c)
}

func writePRTestJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestPullRequestsAvailabilityAndDetail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `[
		  {"number": 7, "title": "ship the panel", "state": "open",
		   "draft": false, "user": {"login": "octo"},
		   "base": {"ref": "main"}, "head": {"ref": "feat/panel"},
		   "html_url": "https://github.com/o/r/pull/7",
		   "updated_at": "2026-09-08T10:00:00Z"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `{
		  "number": 7, "title": "ship the panel", "state": "open",
		  "body": "description body", "draft": false,
		  "user": {"login": "octo"},
		  "base": {"ref": "main", "sha": "b"}, "head": {"ref": "feat/panel", "sha": "h"},
		  "html_url": "https://github.com/o/r/pull/7",
		  "created_at": "2026-09-01T10:00:00Z",
		  "updated_at": "2026-09-08T10:00:00Z",
		  "merged_at": null, "mergeable": true, "mergeable_state": "clean",
		  "changed_files": 1, "additions": 2, "deletions": 1, "commits": 1
		}`)
	})
	mux.HandleFunc("/repos/o/r/pulls/7/commits", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `[]`)
	})
	mux.HandleFunc("/repos/o/r/issues/7/comments", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `[]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `[]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/7/comments", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `[]`)
	})
	mux.HandleFunc("/repos/o/r/commits/h/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `{"total_count": 0, "check_runs": []}`)
	})
	mux.HandleFunc("/repos/o/r/commits/h/status", func(w http.ResponseWriter, _ *http.Request) {
		writePRTestJSON(t, w, `{"state": "success", "statuses": []}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := newPRBinding(t, "git@github.com:o/r.git", srv.URL)
	if !b.Availability().Available {
		t.Fatal("Availability = false for a github.com remote with a token")
	}
	pulls, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 ||
		pulls[0].Head != "feat/panel" {
		t.Fatalf("List = %+v", pulls)
	}
	d, err := b.Detail(7)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "ship the panel" || d.Body != "description body" ||
		d.HeadSHA != "h" || d.CommitsCount != 1 {
		t.Fatalf("Detail = %+v", d)
	}
}

func TestPullRequestsHiddenWithoutGithubRemote(t *testing.T) {
	// Local-only repository: no PR view.
	b := newPRBinding(t, "", "")
	if b.Availability().Available {
		t.Fatal("Availability = true without an origin remote")
	}
	if _, err := b.List(); err == nil {
		t.Fatal("List without a GitHub remote unexpectedly succeeded")
	}

	// Non-GitHub host: hidden even though a token provider exists.
	gitlab := newPRBinding(t, "git@gitlab.com:o/r.git", "")
	if gitlab.Availability().Available {
		t.Fatal("Availability = true for a gitlab.com remote")
	}
}

func TestPullRequestsHiddenOutsideRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), root)
	c.Shell.SetContext(context.Background())
	b := NewPullRequestsBinding(c)
	if b.Availability().Available {
		t.Fatal("Availability = true outside a repository")
	}
}
