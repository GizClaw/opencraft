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

// TestThreadsFromGHGroupsNestedReplies pins reply-of-reply folding:
// GitHub reports every reply with the id of the comment it directly
// answers, so a nested reply whose parent is itself a reply must walk
// up to the thread's root instead of becoming an orphan thread.
func TestThreadsFromGHGroupsNestedReplies(t *testing.T) {
	line := 12
	rootID, replyID, nestedID := int64(501), int64(502), int64(503)
	rootTime := "2026-09-01T10:00:00Z"
	comments := []ghReviewComment{
		{
			ID: rootID, User: ghUser{Login: "alice"},
			Body: "root comment", Path: "panel.go",
			Side: "RIGHT", Line: &line, CreatedAt: rootTime,
		},
		{
			ID: replyID, User: ghUser{Login: "bob"},
			Body: "first reply", Path: "panel.go",
			Side: "RIGHT", Line: &line, InReplyToID: &rootID,
			CreatedAt: "2026-09-01T10:01:00Z",
		},
		{
			ID: nestedID, User: ghUser{Login: "alice"},
			Body: "reply to the reply", Path: "panel.go",
			Side: "RIGHT", Line: &line, InReplyToID: &replyID,
			CreatedAt: "2026-09-01T10:02:00Z",
		},
	}
	threads := threadsFromGH(comments)
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1 (nested reply must fold into its root)", len(threads))
	}
	got := threads[0].Comments
	if len(got) != 3 {
		t.Fatalf("thread comments = %d, want 3", len(got))
	}
	for i, want := range []string{"root comment", "first reply", "reply to the reply"} {
		if got[i].Body != want {
			t.Fatalf("comment %d body = %q, want %q", i, got[i].Body, want)
		}
	}
}

// TestThreadsFromGHKeepsUnresolvableReply tests the orphan fallback:
// a reply whose parent appears in no page stays visible as its own
// thread instead of being dropped.
func TestThreadsFromGHKeepsUnresolvableReply(t *testing.T) {
	line := 4
	missing := int64(9001)
	comments := []ghReviewComment{
		{
			ID: 600, User: ghUser{Login: "carol"},
			Body: "orphan reply", Path: "other.go",
			Side: "LEFT", Line: &line, InReplyToID: &missing,
			CreatedAt: "2026-09-02T10:00:00Z",
		},
	}
	threads := threadsFromGH(comments)
	if len(threads) != 1 || len(threads[0].Comments) != 1 ||
		threads[0].Comments[0].Body != "orphan reply" {
		t.Fatalf("orphan reply threads = %+v, want one preserved comment", threads)
	}
}

// TestChecksCompletedWithoutConclusionIsNeutral pins that a completed
// check run with a null conclusion renders neutral (not an endless
// pending spinner), while a genuinely in-progress run stays pending.
func TestChecksCompletedWithoutConclusionIsNeutral(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits/abc/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"total_count": 2, "check_runs": [
		  {"name": "null-done", "status": "completed", "conclusion": null,
		   "details_url": ""},
		  {"name": "still-going", "status": "in_progress", "conclusion": null,
		   "details_url": ""}
		]}`)
	})
	mux.HandleFunc("/repos/o/r/commits/abc/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state": "success", "statuses": []}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &apiClient{base: srv.URL, http: srv.Client(), token: "tok"}
	checks, err := c.checks(context.Background(), "o", "r", "abc")
	if err != nil {
		t.Fatalf("checks: %v", err)
	}
	var neutral, pending bool
	for _, ch := range checks {
		switch ch.Name {
		case "null-done":
			neutral = ch.State == CheckNeutral
		case "still-going":
			pending = ch.State == CheckPending
		}
	}
	if !neutral || !pending {
		t.Fatalf("checks = %+v, want null-done=neutral and still-going=pending", checks)
	}
}

// TestChecksFallBackToLegacyStatuses covers the 404 degradation path:
// repositories without check suites still surface their legacy commit
// statuses instead of failing the PR page.
func TestChecksFallBackToLegacyStatuses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits/abc/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message": "Not Found"}`)
	})
	mux.HandleFunc("/repos/o/r/commits/abc/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state": "failure", "statuses": [
		  {"context": "ci-legacy", "state": "failure",
		   "description": "integration failed", "target_url": ""}
		]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &apiClient{base: srv.URL, http: srv.Client(), token: "tok"}
	checks, err := c.checks(context.Background(), "o", "r", "abc")
	if err != nil {
		t.Fatalf("checks fallback: %v", err)
	}
	if len(checks) != 1 || checks[0].Kind != "status" ||
		checks[0].Name != "ci-legacy" || checks[0].State != CheckFailure {
		t.Fatalf("fallback checks = %+v, want one legacy failure status", checks)
	}
}

// commitRows renders n commit JSON objects for a pagination fixture.
func commitRows(n int) string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf(
			`{"sha": "%040d", "commit": {"author": {"name": "alice", `+
				`"date": "2026-01-01T00:00:00Z"}, "message": "commit %d"}}`,
			i, i))
	}
	return "[" + strings.Join(out, ",") + "]"
}

// newCommitPager serves totalPages pages of commits; moreOnLast adds a
// Link header pointing at one further page so clients know more data
// exists beyond the page cap.
func newCommitPager(t *testing.T, totalPages int, moreOnLast bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/o/r/pulls/1/commits", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		num := 1
		if page != "" {
			if _, err := fmt.Sscanf(page, "%d", &num); err != nil {
				t.Fatalf("parse page %q: %v", page, err)
			}
		}
		if num > totalPages {
			writeJSON(t, w, "[]")
			return
		}
		if num < totalPages || moreOnLast {
			w.Header().Set("Link", fmt.Sprintf(
				`<%s/repos/o/r/pulls/1/commits?page=%d&per_page=100>; rel="next"`,
				srv.URL, num+1))
		}
		writeJSON(t, w, commitRows(3))
	})
	srv = httptest.NewServer(mux)
	return srv
}

func TestCommitsPaginationTruncationSemantics(t *testing.T) {
	// Natural end after exactly maxCommitPages pages: nothing follows
	// the third page, so the snapshot must not be flagged truncated.
	srv := newCommitPager(t, 3, false)
	defer srv.Close()
	c := &apiClient{base: srv.URL, http: srv.Client(), token: "tok"}
	commits, err := c.commits(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("commits: %v", err)
	}
	if len(commits) != 9 {
		t.Fatalf("commits = %d, want 9", len(commits))
	}
	if c.commitsTruncated {
		t.Fatal("natural three-page end was flagged as truncated")
	}

	// A fourth page advertised after the cap: the fetch stops at three
	// pages and must report truncation.
	srv = newCommitPager(t, 4, true)
	defer srv.Close()
	c = &apiClient{base: srv.URL, http: srv.Client(), token: "tok"}
	commits, err = c.commits(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("commits capped: %v", err)
	}
	if len(commits) != 9 {
		t.Fatalf("capped commits = %d, want 9 (three pages)", len(commits))
	}
	if !c.commitsTruncated {
		t.Fatal("page cap hit with more pages advertised was not flagged")
	}
}
