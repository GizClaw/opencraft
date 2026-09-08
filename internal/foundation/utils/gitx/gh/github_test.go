package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testPRJSON = `{
  "number": 42,
  "title": "Add the PR panel",
  "state": "open",
  "body": "This PR adds the panel.",
  "draft": false,
  "user": {"login": "octo", "avatar_url": "https://avatars/x.png"},
  "base": {"ref": "main", "sha": "base-sha"},
  "head": {"ref": "feat/panel", "sha": "head-sha"},
  "html_url": "https://github.com/o/r/pull/42",
  "created_at": "2026-09-01T10:00:00Z",
  "updated_at": "2026-09-08T10:00:00Z",
  "merged_at": null,
  "mergeable": true,
  "mergeable_state": "clean",
  "changed_files": 3,
  "additions": 40,
  "deletions": 12,
  "commits": 2
}`

func newTestService(t *testing.T, tokenFn func(context.Context) (string, error)) (*Service, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `[
		  {"number": 42, "title": "Add the PR panel", "state": "open",
		   "draft": false, "user": {"login": "octo"},
		   "base": {"ref": "main"}, "head": {"ref": "feat/panel"},
		   "html_url": "https://github.com/o/r/pull/42",
		   "updated_at": "2026-09-08T10:00:00Z"},
		  {"number": 43, "title": "Merged cleanup", "state": "closed",
		   "draft": false, "user": {"login": "octo"},
		   "base": {"ref": "main"}, "head": {"ref": "cleanup"},
		   "merged_at": "2026-08-01T10:00:00Z",
		   "html_url": "https://github.com/o/r/pull/43",
		   "updated_at": "2026-08-02T10:00:00Z"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, testPRJSON)
	})
	mux.HandleFunc("/repos/o/r/pulls/42/commits", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `[
		  {"sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		   "commit": {"author": {"name": "Alice", "date": "2026-09-01T10:00:00Z"},
		              "message": "feat: panel"}},
		  {"sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		   "commit": {"author": {"name": "Bob", "date": "2026-09-02T10:00:00Z"},
		              "message": "fix: review notes"}}
		]`)
	})
	mux.HandleFunc("/repos/o/r/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `[
		  {"id": 900, "user": {"login": "carol"},
		   "body": "When does this ship?",
		   "created_at": "2026-09-03T10:00:00Z",
		   "html_url": "https://github.com/o/r/pull/42#issuecomment-900"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `[
		  {"id": 11, "user": {"login": "dave"}, "state": "PENDING",
		   "body": "not submitted yet", "submitted_at": ""},
		  {"id": 12, "user": {"login": "erin"}, "state": "APPROVED",
		   "body": "lgtm", "submitted_at": "2026-09-04T10:00:00Z",
		   "html_url": "https://github.com/o/r/pull/42#pullrequestreview-12"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/42/comments", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `[
		  {"id": 201, "user": {"login": "erin"},
		   "body": "Please handle empty state", "path": "panel.go",
		   "diff_hunk": "@@ -8,6 +8,8 @@ func Render() {\n text := body\n+if text == \"\" {\n+  return\n+}\n return text\n }",
		   "side": "RIGHT", "line": 10, "original_line": 8,
		   "created_at": "2026-09-04T10:01:00Z",
		   "html_url": "https://github.com/o/r/pull/42#discussion_r201"},
		  {"id": 202, "user": {"login": "alice"},
		   "body": "done", "path": "panel.go",
		   "diff_hunk": "", "side": "RIGHT", "line": 10,
		   "in_reply_to_id": 201,
		   "created_at": "2026-09-05T10:00:00Z",
		   "html_url": "https://github.com/o/r/pull/42#discussion_r202"},
		  {"id": 203, "user": {"login": "erin"},
		   "body": "old side note", "path": "legacy.go",
		   "diff_hunk": "@@ -3,7 +3,6 @@ func Old() {\n return 1\n-return 2\n }",
		   "side": "LEFT", "line": 5, "original_line": 4,
		   "created_at": "2026-09-04T11:00:00Z",
		   "html_url": "https://github.com/o/r/pull/42#discussion_r203"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/commits/head-sha/check-runs", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `{"total_count": 2, "check_runs": [
		  {"name": "unit", "status": "completed", "conclusion": "success",
		   "details_url": "https://github.com/o/r/actions/runs/1"},
		  {"name": "e2e", "status": "in_progress", "conclusion": null,
		   "details_url": ""}
		]}`)
	})
	mux.HandleFunc("/repos/o/r/commits/head-sha/status", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		writeJSON(t, w, `{"state": "failure", "statuses": [
		  {"context": "ci-legacy", "state": "failure",
		   "description": "integration failed", "target_url": ""}
		]}`)
	})
	srv := httptest.NewServer(mux)
	svc := NewService(Options{
		BaseURL: srv.URL,
		TokenFn: tokenFn,
	})
	return svc, srv
}

func tokenFn(_ context.Context) (string, error) {
	return "tok-test", nil
}

func checkAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer tok-test" {
		t.Errorf("Authorization = %q, want Bearer tok-test", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestServiceListAndDetail(t *testing.T) {
	svc, srv := newTestService(t, tokenFn)
	defer srv.Close()
	ctx := context.Background()

	pulls, err := svc.List(ctx, "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 2 {
		t.Fatalf("List = %+v, want 2 pulls", pulls)
	}
	if pulls[0].State != "open" || pulls[0].Number != 42 {
		t.Fatalf("first pull = %+v", pulls[0])
	}
	if pulls[1].State != "merged" {
		t.Fatalf("closed PR state = %q, want merged", pulls[1].State)
	}

	d, err := svc.Detail(ctx, "o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Add the PR panel" || d.Body == "" ||
		d.Head != "feat/panel" || d.HeadSHA != "head-sha" {
		t.Fatalf("Detail header = %+v", d)
	}
	if d.MergeableState != "clean" || !d.Mergeable ||
		d.Additions != 40 || d.ChangedFiles != 3 {
		t.Fatalf("Detail stats = %+v", d)
	}
	if len(d.Commits) != 2 || d.Commits[0].ShortSHA != "aaaaaaa" {
		t.Fatalf("Detail commits = %+v", d.Commits)
	}
	if d.Truncated {
		t.Fatal("Detail truncated for a small fixture")
	}

	// CI: failure (legacy status) must sort before pending, success.
	if len(d.Checks) != 3 || d.Checks[0].Name != "ci-legacy" ||
		d.Checks[0].State != CheckFailure ||
		d.Checks[1].State != CheckPending ||
		d.Checks[2].State != CheckSuccess {
		t.Fatalf("Detail checks = %+v", d.Checks)
	}

	// Conversation: pending review is dropped; comment and review are
	// chronological.
	if len(d.Conversation) != 2 ||
		d.Conversation[0].Kind != "comment" ||
		d.Conversation[1].Action != "APPROVED" {
		t.Fatalf("Conversation = %+v", d.Conversation)
	}

	if len(d.Threads) != 2 {
		t.Fatalf("Threads = %+v, want 2", d.Threads)
	}
	left := d.Threads[0]
	if left.Path != "legacy.go" || left.Side != "LEFT" ||
		left.OriginalLine != 4 {
		t.Fatalf("left thread = %+v", left)
	}
	right := d.Threads[1]
	if right.Path != "panel.go" || right.Side != "RIGHT" ||
		right.Line != 10 || right.DiffHunk == "" {
		t.Fatalf("right thread = %+v", right)
	}
	if len(right.Comments) != 2 || right.Comments[0].Body !=
		"Please handle empty state" || right.Comments[1].Body != "done" {
		t.Fatalf("thread comments = %+v", right.Comments)
	}
}

func TestServiceProviderGating(t *testing.T) {
	svc, srv := newTestService(t,
		func(context.Context) (string, error) {
			return "", errors.New("gh not logged in")
		})
	defer srv.Close()
	ctx := context.Background()
	if svc.Available(ctx) {
		t.Fatal("Available = true without a token")
	}
	if _, err := svc.List(ctx, "o", "r"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("List error = %v, want ErrNoProvider", err)
	}
}

func TestServiceAvailabilityCacheAndOwnerValidation(t *testing.T) {
	var calls atomic.Int32
	svc := NewService(Options{
		TokenFn: func(context.Context) (string, error) {
			calls.Add(1)
			return "tok", nil
		},
	})
	ctx := context.Background()
	if !svc.Available(ctx) {
		t.Fatal("Available = false with working token")
	}
	if !svc.Available(ctx) {
		t.Fatal("second Available call flipped to false")
	}
	if !svc.Available(ctx) {
		t.Fatal("cached Available flipped")
	}
	if calls.Load() != 1 {
		t.Fatalf("token resolved %d times, want 1 (cached)", calls.Load())
	}
	if _, err := svc.List(ctx, "../evil", "r"); err == nil {
		t.Fatal("List accepted an invalid owner")
	}
}

func TestServiceHTTPErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/999", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message": "Not Found"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := NewService(Options{BaseURL: srv.URL, TokenFn: tokenFn})
	_, err := svc.Detail(context.Background(), "o", "r", 999)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Detail error = %v, want not-found message", err)
	}
}
