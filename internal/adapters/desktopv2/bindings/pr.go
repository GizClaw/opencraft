package bindings

import (
	"errors"
	"strings"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
	gitxgh "github.com/GizClaw/opencraft/internal/foundation/utils/gitx/gh"
)

// PullRequests exposes the GitHub pull-request view for the Git panel.
// The binding stays thin: it resolves the repository remote (origin)
// and delegates every network/credential concern to the remote
// abstraction returned by core.Git.Remote(). No token ever crosses the
// binding boundary.
type PullRequests struct {
	core *core.Core
}

// NewPullRequestsBinding wires the GitHub PR binding.
func NewPullRequestsBinding(c *core.Core) *PullRequests {
	return &PullRequests{core: c}
}

// PRAvailabilityDTO tells the UI whether the PR segment may render.
type PRAvailabilityDTO struct {
	Available bool `json:"available"`
}

// Availability probes the active workspace's repository: it must be
// inside a git repo whose origin is GitHub.com, and gh must be
// installed and logged in for github.com. Missing pieces simply report
// false so the UI hides the PR view (no provider = no PR page).
func (b *PullRequests) Availability() PRAvailabilityDTO {
	ctx := b.core.Shell.Context()
	_, ok := b.githubRemote()
	if !ok {
		return PRAvailabilityDTO{Available: false}
	}
	if !b.core.Git.Remote().Available(ctx) {
		return PRAvailabilityDTO{Available: false}
	}
	return PRAvailabilityDTO{Available: true}
}

// GitHubAuthorDTO is one GitHub user.
type GitHubAuthorDTO struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// GitHubPullDTO is one row of the PR list.
type GitHubPullDTO struct {
	Number    int             `json:"number"`
	Title     string          `json:"title"`
	State     string          `json:"state"`
	Draft     bool            `json:"draft"`
	Author    GitHubAuthorDTO `json:"author"`
	Base      string          `json:"base"`
	Head      string          `json:"head"`
	UpdatedAt string          `json:"updated_at"`
	HTMLURL   string          `json:"html_url"`
}

// List returns the open pull requests of the active repository.
func (b *PullRequests) List() ([]GitHubPullDTO, error) {
	owner, repo, err := b.requireGithubRemote()
	if err != nil {
		return nil, err
	}
	pulls, err := b.core.Git.Remote().List(
		b.core.Shell.Context(), owner, repo)
	if err != nil {
		return nil, err
	}
	out := make([]GitHubPullDTO, 0, len(pulls))
	for _, p := range pulls {
		out = append(out, GitHubPullDTO{
			Number: p.Number,
			Title:  p.Title,
			State:  p.State,
			Draft:  p.Draft,
			Author: GitHubAuthorDTO{
				Login:     p.Author.Login,
				AvatarURL: p.Author.AvatarURL,
			},
			Base:      p.Base,
			Head:      p.Head,
			UpdatedAt: p.UpdatedAt,
			HTMLURL:   p.HTMLURL,
		})
	}
	return out, nil
}

// GitHubPRCommitDTO is one commit row of the PR page.
type GitHubPRCommitDTO struct {
	SHA      string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	Message  string `json:"message"`
	Author   string `json:"author"`
	Date     string `json:"date"`
}

// GitHubCheckDTO is one normalized CI entry.
type GitHubCheckDTO struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// GitHubCommentDTO is one comment in a timeline or thread.
type GitHubCommentDTO struct {
	ID        int64           `json:"id"`
	Author    GitHubAuthorDTO `json:"author"`
	Body      string          `json:"body"`
	CreatedAt string          `json:"created_at"`
	HTMLURL   string          `json:"html_url"`
}

// GitHubTimelineDTO is one conversation entry.
type GitHubTimelineDTO struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Author    GitHubAuthorDTO `json:"author"`
	Body      string          `json:"body"`
	Action    string          `json:"action,omitempty"`
	CreatedAt string          `json:"created_at"`
	HTMLURL   string          `json:"html_url"`
}

// GitHubThreadDTO is one anchored inline review discussion.
type GitHubThreadDTO struct {
	Path              string             `json:"path"`
	Side              string             `json:"side,omitempty"`
	Line              int                `json:"line"`
	OriginalLine      int                `json:"original_line"`
	StartLine         int                `json:"start_line,omitempty"`
	OriginalStartLine int                `json:"original_start_line,omitempty"`
	DiffHunk          string             `json:"diff_hunk"`
	Comments          []GitHubCommentDTO `json:"comments"`
}

// GitHubPRDetailDTO is the full PR page payload.
type GitHubPRDetailDTO struct {
	Number         int                 `json:"number"`
	Title          string              `json:"title"`
	State          string              `json:"state"`
	Draft          bool                `json:"draft"`
	Author         GitHubAuthorDTO     `json:"author"`
	Base           string              `json:"base"`
	Head           string              `json:"head"`
	HeadSHA        string              `json:"head_sha"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	HTMLURL        string              `json:"html_url"`
	Body           string              `json:"body"`
	Mergeable      bool                `json:"mergeable"`
	MergeableState string              `json:"mergeable_state"`
	ChangedFiles   int                 `json:"changed_files"`
	Additions      int                 `json:"additions"`
	Deletions      int                 `json:"deletions"`
	CommitsCount   int                 `json:"commits_count"`
	Commits        []GitHubPRCommitDTO `json:"commits"`
	Checks         []GitHubCheckDTO    `json:"checks"`
	Conversation   []GitHubTimelineDTO `json:"conversation"`
	Threads        []GitHubThreadDTO   `json:"threads"`
	Truncated      bool                `json:"truncated"`
}

// Detail loads one pull request page.
func (b *PullRequests) Detail(
	number int,
) (GitHubPRDetailDTO, error) {
	owner, repo, err := b.requireGithubRemote()
	if err != nil {
		return GitHubPRDetailDTO{}, err
	}
	d, err := b.core.Git.Remote().Detail(
		b.core.Shell.Context(), owner, repo, number)
	if err != nil {
		return GitHubPRDetailDTO{}, err
	}
	return pullDetailDTO(d), nil
}

// githubRemote resolves the origin remote when it is GitHub.com. ok is
// false for non-repository workspaces, non-GitHub hosts or missing
// origins; the UI then hides the PR segment.
func (b *PullRequests) githubRemote() (gitx.RemoteInfo, bool) {
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return gitx.RemoteInfo{}, false
	}
	ctx := b.core.Shell.Context()
	info := gitx.Info(ctx, workDir)
	if info.Root == "" {
		return gitx.RemoteInfo{}, false
	}
	remote, ok := gitx.Remote(ctx, info.Root)
	if !ok || !strings.EqualFold(remote.Host, "github.com") {
		return gitx.RemoteInfo{}, false
	}
	return remote, true
}

// requireGithubRemote is githubRemote with an error for List/Detail so
// direct calls surface a readable reason instead of an empty list.
func (b *PullRequests) requireGithubRemote() (string, string, error) {
	remote, ok := b.githubRemote()
	if !ok {
		return "", "", errors.New(
			"github: the active repository is not hosted on GitHub.com " +
				"(origin remote required)")
	}
	return remote.Owner, remote.Repo, nil
}

func pullDetailDTO(d *gitxgh.Detail) GitHubPRDetailDTO {
	if d == nil {
		return GitHubPRDetailDTO{}
	}
	return GitHubPRDetailDTO{
		Number: d.Number,
		Title:  d.Title,
		State:  d.State,
		Draft:  d.Draft,
		Author: GitHubAuthorDTO{
			Login: d.Author.Login, AvatarURL: d.Author.AvatarURL,
		},
		Base:           d.Base,
		Head:           d.Head,
		HeadSHA:        d.HeadSHA,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
		HTMLURL:        d.HTMLURL,
		Body:           d.Body,
		Mergeable:      d.Mergeable,
		MergeableState: d.MergeableState,
		ChangedFiles:   d.ChangedFiles,
		Additions:      d.Additions,
		Deletions:      d.Deletions,
		CommitsCount:   d.CommitsCount,
		Commits:        commitsDTO(d.Commits),
		Checks:         checksDTO(d.Checks),
		Conversation:   timelineDTO(d.Conversation),
		Threads:        threadsDTO(d.Threads),
		Truncated:      d.Truncated,
	}
}

func commitsDTO(commits []gitxgh.Commit) []GitHubPRCommitDTO {
	out := make([]GitHubPRCommitDTO, 0, len(commits))
	for _, c := range commits {
		out = append(out, GitHubPRCommitDTO{
			SHA:      c.SHA,
			ShortSHA: c.ShortSHA,
			Message:  c.Message,
			Author:   c.Author,
			Date:     c.Date,
		})
	}
	return out
}

func checksDTO(checks []gitxgh.Check) []GitHubCheckDTO {
	out := make([]GitHubCheckDTO, 0, len(checks))
	for _, c := range checks {
		out = append(out, GitHubCheckDTO{
			Name:        c.Name,
			Kind:        c.Kind,
			State:       string(c.State),
			Description: c.Description,
			URL:         c.URL,
		})
	}
	return out
}

func timelineDTO(items []gitxgh.TimelineItem) []GitHubTimelineDTO {
	out := make([]GitHubTimelineDTO, 0, len(items))
	for _, it := range items {
		out = append(out, GitHubTimelineDTO{
			ID:   it.ID,
			Kind: it.Kind,
			Author: GitHubAuthorDTO{
				Login: it.Author.Login, AvatarURL: it.Author.AvatarURL,
			},
			Body:      it.Body,
			Action:    it.Action,
			CreatedAt: it.CreatedAt,
			HTMLURL:   it.HTMLURL,
		})
	}
	return out
}

func threadsDTO(threads []gitxgh.Thread) []GitHubThreadDTO {
	out := make([]GitHubThreadDTO, 0, len(threads))
	for _, t := range threads {
		comments := make([]GitHubCommentDTO, 0, len(t.Comments))
		for _, c := range t.Comments {
			comments = append(comments, GitHubCommentDTO{
				ID: c.ID,
				Author: GitHubAuthorDTO{
					Login: c.Author.Login, AvatarURL: c.Author.AvatarURL,
				},
				Body:      c.Body,
				CreatedAt: c.CreatedAt,
				HTMLURL:   c.HTMLURL,
			})
		}
		out = append(out, GitHubThreadDTO{
			Path:              t.Path,
			Side:              t.Side,
			Line:              t.Line,
			OriginalLine:      t.OriginalLine,
			StartLine:         t.StartLine,
			OriginalStartLine: t.OriginalStartLine,
			DiffHunk:          t.DiffHunk,
			Comments:          comments,
		})
	}
	return out
}
