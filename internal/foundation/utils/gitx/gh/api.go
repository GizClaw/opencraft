package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// apiClient talks to one GitHub REST batch under a single token. It is
// created per List/Detail call, so truncation flags never leak between
// operations.
type apiClient struct {
	base  string
	token string
	http  *http.Client

	commitsTruncated bool
	issueTruncated   bool
	inlineTruncated  bool
}

const (
	maxBodyBytes = 16 << 20
	// Page caps keep one detail payload bounded even when a PR has
	// thousands of comments or commits.
	maxCommitPages  = 3 // <= 300 commits
	maxCommentPages = 5 // <= 500 comments per list
)

func newAPIClient(base string, httpClient *http.Client, token string) *apiClient {
	return &apiClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  httpClient,
	}
}

// ghError is GitHub's error envelope; the message is safe for the UI
// (GitHub never puts credentials in it).
type ghError struct {
	Message string `json:"message"`
}

// getBody performs one GET and returns the capped body plus the next
// page URL from the Link header. Errors are classified by status.
func (c *apiClient) getBody(
	ctx context.Context,
	rawURL string,
) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", errf("build request", err.Error())
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "OpenCraft")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", errf("request failed", err.Error())
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			return // body fully read above; close failure is not actionable
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, "", errf("read response", err.Error())
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, "", errf("response too large", "")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := http.StatusText(resp.StatusCode)
		var ge ghError
		if json.Unmarshal(body, &ge) == nil && ge.Message != "" {
			msg = ge.Message
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, "", errors.New(
				"github: provider token rejected; run `gh auth login` again")
		case http.StatusNotFound:
			return nil, "", errors.New(
				"github: pull request or repository not found")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, "", errf("GitHub rate limit reached",
				"wait a moment and retry, or check `gh auth status`")
		default:
			return nil, "", errf(
				"GitHub request failed", fmt.Sprintf("%s (%d)", msg,
					resp.StatusCode))
		}
	}
	return body, nextLink(resp.Header.Get("Link")), nil
}

// firstURL builds the initial page URL for one API path.
func (c *apiClient) firstURL(
	path string,
	params url.Values,
) string {
	if params == nil {
		params = url.Values{}
	}
	return c.base + path + "?" + params.Encode()
}

// collect walks at most maxPages pages, decoding each into page.
func (c *apiClient) collect(
	ctx context.Context,
	path string,
	params url.Values,
	maxPages int,
	page func([]byte) error,
) error {
	next := c.firstURL(path, params)
	pages := 0
	for next != "" {
		if pages >= maxPages {
			return nil
		}
		body, link, err := c.getBody(ctx, next)
		if err != nil {
			return err
		}
		pages++
		if err := page(body); err != nil {
			return errf("decode response", err.Error())
		}
		next = link
	}
	return nil
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

func nextLink(header string) string {
	m := linkNextRe.FindStringSubmatch(header)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// ---- raw GitHub models ----

type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type ghRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type ghPull struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
	User   ghUser `json:"user"`

	Base ghRef `json:"base"`
	Head ghRef `json:"head"`

	HTMLURL        string  `json:"html_url"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	MergedAt       *string `json:"merged_at"`
	Mergeable      *bool   `json:"mergeable"`
	MergeableState string  `json:"mergeable_state"`
	ChangedFiles   int     `json:"changed_files"`
	Additions      int     `json:"additions"`
	Deletions      int     `json:"deletions"`
	Commits        int     `json:"commits"`
}

type ghCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
		Message string `json:"message"`
	} `json:"commit"`
}

type ghIssueComment struct {
	ID        int64  `json:"id"`
	User      ghUser `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
}

type ghReview struct {
	ID          int64  `json:"id"`
	User        ghUser `json:"user"`
	State       string `json:"state"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
	HTMLURL     string `json:"html_url"`
}

type ghReviewComment struct {
	ID        int64  `json:"id"`
	User      ghUser `json:"user"`
	Body      string `json:"body"`
	Path      string `json:"path"`
	DiffHunk  string `json:"diff_hunk"`
	Side      string `json:"side"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`

	Line              *int   `json:"line"`
	OriginalLine      *int   `json:"original_line"`
	StartLine         *int   `json:"start_line"`
	OriginalStartLine *int   `json:"original_start_line"`
	InReplyToID       *int64 `json:"in_reply_to_id"`
}

type ghCheckRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	DetailsURL string  `json:"details_url"`
}

type ghChecks struct {
	CheckRuns []ghCheckRun `json:"check_runs"`
}

type ghStatus struct {
	Context     string `json:"context"`
	State       string `json:"state"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
}

type ghStatuses struct {
	State    string     `json:"state"`
	Statuses []ghStatus `json:"statuses"`
}

// ---- endpoint fetchers ----

func (c *apiClient) listPRs(
	ctx context.Context,
	owner, repo string,
) ([]Pull, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls", owner, repo)
	params := url.Values{}
	params.Set("state", "open")
	params.Set("sort", "updated")
	params.Set("direction", "desc")
	params.Set("per_page", "100")
	var pulls []ghPull
	err := c.collect(ctx, path, params, 1,
		func(body []byte) error {
			return json.Unmarshal(body, &pulls)
		})
	if err != nil {
		return nil, err
	}
	out := make([]Pull, 0, len(pulls))
	for _, p := range pulls {
		out = append(out, pullFromGH(p))
	}
	return out, nil
}

func (c *apiClient) pull(
	ctx context.Context,
	owner, repo string,
	number int,
) (ghPull, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	body, _, err := c.getBody(ctx, c.firstURL(path, nil))
	if err != nil {
		return ghPull{}, err
	}
	var p ghPull
	if err := json.Unmarshal(body, &p); err != nil {
		return ghPull{}, errf("decode pull request", err.Error())
	}
	return p, nil
}

func (c *apiClient) commits(
	ctx context.Context,
	owner, repo string,
	number int,
) ([]ghCommit, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/commits", owner, repo, number)
	params := url.Values{}
	params.Set("per_page", "100")
	var commits []ghCommit
	pages := 0
	err := c.collect(ctx, path, params, maxCommitPages,
		func(body []byte) error {
			pages++
			var batch []ghCommit
			if err := json.Unmarshal(body, &batch); err != nil {
				return err
			}
			commits = append(commits, batch...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	// collect() cannot distinguish "hit the page cap" from "no next
	// page", so the caller cross-checks against the PR-reported commit
	// count; this flag only covers the hard page cap itself.
	c.commitsTruncated = pages >= maxCommitPages
	return commits, nil
}

func (c *apiClient) issueComments(
	ctx context.Context,
	owner, repo string,
	number int,
) ([]ghIssueComment, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	params := url.Values{}
	params.Set("per_page", "100")
	var comments []ghIssueComment
	pages := 0
	err := c.collect(ctx, path, params, maxCommentPages,
		func(body []byte) error {
			pages++
			var batch []ghIssueComment
			if err := json.Unmarshal(body, &batch); err != nil {
				return err
			}
			comments = append(comments, batch...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	c.issueTruncated = pages >= maxCommentPages
	return comments, nil
}

func (c *apiClient) reviews(
	ctx context.Context,
	owner, repo string,
	number int,
) ([]ghReview, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	params := url.Values{}
	params.Set("per_page", "100")
	var reviews []ghReview
	err := c.collect(ctx, path, params, 1,
		func(body []byte) error {
			return json.Unmarshal(body, &reviews)
		})
	if err != nil {
		return nil, err
	}
	return reviews, nil
}

func (c *apiClient) reviewComments(
	ctx context.Context,
	owner, repo string,
	number int,
) ([]ghReviewComment, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", owner, repo, number)
	params := url.Values{}
	params.Set("per_page", "100")
	var comments []ghReviewComment
	pages := 0
	err := c.collect(ctx, path, params, maxCommentPages,
		func(body []byte) error {
			pages++
			var batch []ghReviewComment
			if err := json.Unmarshal(body, &batch); err != nil {
				return err
			}
			comments = append(comments, batch...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	c.inlineTruncated = pages >= maxCommentPages
	return comments, nil
}

func (c *apiClient) checks(
	ctx context.Context,
	owner, repo, sha string,
) ([]Check, error) {
	var out []Check
	// Check Runs API first; older repositories without check suites
	// still get their legacy statuses merged in below.
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", owner, repo, sha)
	params := url.Values{}
	params.Set("per_page", "100")
	var runs ghChecks
	err := c.collect(ctx, path, params, 1,
		func(body []byte) error {
			return json.Unmarshal(body, &runs)
		})
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	for _, r := range runs.CheckRuns {
		state := CheckPending
		if r.Status == "completed" && r.Conclusion != nil {
			state = CheckState(*r.Conclusion)
		}
		out = append(out, Check{
			Name:  r.Name,
			Kind:  "check_run",
			State: state,
			URL:   r.DetailsURL,
		})
	}

	statusPath := fmt.Sprintf("/repos/%s/%s/commits/%s/status",
		owner, repo, sha)
	var st ghStatuses
	err = c.collect(ctx, statusPath, nil, 1,
		func(body []byte) error {
			return json.Unmarshal(body, &st)
		})
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	for _, s := range st.Statuses {
		out = append(out, Check{
			Name:        s.Context,
			Kind:        "status",
			State:       CheckState(s.State),
			Description: s.Description,
			URL:         s.TargetURL,
		})
	}
	// Failed and pending checks surface first; everything else trails.
	rank := map[CheckState]int{
		CheckFailure:      0,
		CheckError:        0,
		CheckActionNeeded: 0,
		CheckTimedOut:     0,
		CheckPending:      1,
		CheckSuccess:      2,
		CheckSkipped:      3,
		CheckNeutral:      3,
		CheckStale:        3,
		CheckCancelled:    3,
	}
	sort.SliceStable(out, func(i, j int) bool {
		return rank[out[i].State] < rank[out[j].State]
	})
	return out, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
