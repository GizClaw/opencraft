// Package gh is the GitHub.com implementation behind the Git panel's
// PR view. It is pure infrastructure with no domain state: read-only,
// bounded, and does not know about adapters or orchestration. The
// provider contract it satisfies (core's Remote abstraction) lives at
// the consumer, so future hosts can implement the same contract without
// importing GitHub-specific code. Authentication follows the lazygit
// model (ask `gh auth token` for every batch, never cache a token
// beyond the batch, never put it on the wire through the UI).
package gh

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// ErrNoProvider is returned when no usable GitHub provider exists: gh
// is not installed, not logged in to github.com, or refuses to hand
// out a token. Callers treat it as "hide the PR view".
var ErrNoProvider = errors.New(
	"github: no authenticated GitHub provider (install and run `gh auth login`)")

// nameRe mirrors GitHub's owner/repository naming rules. It is reused
// for both remote segments so nothing unsanitized reaches an API path.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Author is one GitHub user rendered by the PR view.
type Author struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// Pull is one row of the PR list and the header of the detail page.
type Pull struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"` // "open", "closed" or "merged"
	Draft     bool   `json:"draft"`
	Author    Author `json:"author"`
	Base      string `json:"base"`
	Head      string `json:"head"`
	HeadSHA   string `json:"head_sha,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	HTMLURL   string `json:"html_url"`
}

// Commit is one row of the PR commit list.
type Commit struct {
	SHA      string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	Message  string `json:"message"`
	Author   string `json:"author"`
	Date     string `json:"date"`
}

// CheckState is the normalized CI conclusion used by the UI.
type CheckState string

const (
	CheckSuccess      CheckState = "success"
	CheckFailure      CheckState = "failure"
	CheckPending      CheckState = "pending"
	CheckError        CheckState = "error"
	CheckCancelled    CheckState = "cancelled"
	CheckSkipped      CheckState = "skipped"
	CheckNeutral      CheckState = "neutral"
	CheckActionNeeded CheckState = "action_required"
	CheckTimedOut     CheckState = "timed_out"
	CheckStale        CheckState = "stale"
)

// Check is one normalized CI entry (Check Runs API or legacy Statuses
// API) attached to the PR head commit.
type Check struct {
	Name        string     `json:"name"`
	Kind        string     `json:"kind"` // "check_run" or "status"
	State       CheckState `json:"state"`
	Description string     `json:"description,omitempty"`
	URL         string     `json:"url,omitempty"`
}

// Comment is a conversation or inline thread comment.
type Comment struct {
	ID        int64  `json:"id"`
	Author    Author `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
}

// TimelineItem is one chronological conversation entry: an issue
// comment or a submitted review summary.
type TimelineItem struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"` // "comment" or "review"
	Author    Author `json:"author"`
	Body      string `json:"body"`
	Action    string `json:"action,omitempty"` // review state when Kind is review
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
}

// Thread is one anchored inline review discussion. The code snippet is
// the diff_hunk GitHub already returns on every review comment, so the
// UI reconstructs the exact context the reviewer saw without a second
// blob fetch. Line numbers refer to the side the thread lives on:
// RIGHT threads anchor on new-file lines, LEFT threads on original
// lines (multi-line ranges use the *_start fields).
type Thread struct {
	Path              string    `json:"path"`
	Side              string    `json:"side,omitempty"` // "LEFT" or "RIGHT"
	Line              int       `json:"line"`
	OriginalLine      int       `json:"original_line"`
	StartLine         int       `json:"start_line,omitempty"`
	OriginalStartLine int       `json:"original_start_line,omitempty"`
	DiffHunk          string    `json:"diff_hunk"`
	Comments          []Comment `json:"comments"`
}

// Detail is the full PR page payload: header, commits, CI checks, the
// conversation timeline and inline review threads.
type Detail struct {
	Pull
	Body           string         `json:"body"`
	Mergeable      bool           `json:"mergeable"`
	MergeableState string         `json:"mergeable_state"`
	ChangedFiles   int            `json:"changed_files"`
	Additions      int            `json:"additions"`
	Deletions      int            `json:"deletions"`
	CommitsCount   int            `json:"commits_count"`
	Commits        []Commit       `json:"commits"`
	Checks         []Check        `json:"checks"`
	Conversation   []TimelineItem `json:"conversation"`
	Threads        []Thread       `json:"threads"`
	Truncated      bool           `json:"truncated"`
}

// Options configure a Service. All fields are optional.
type Options struct {
	// BaseURL overrides the GitHub REST base (tests use an httptest
	// server). Defaults to https://api.github.com.
	BaseURL string
	// Client is the HTTP client; a 20s client is created when nil.
	Client *http.Client
	// LookPath resolves the gh binary; defaults to exec.LookPath. When
	// PATH misses, well-known per-platform install locations (Homebrew,
	// distro and snap dirs on Unix, the official GitHub CLI directories
	// on Windows) are probed so GUI-launched apps without a login-shell
	// PATH still find the CLI. GH_PATH pins the binary explicitly.
	LookPath func(string) (string, error)
	// TokenFn supplies a token without spawning gh; tests inject it.
	// When nil the service runs `gh auth token --hostname github.com`.
	TokenFn func(context.Context) (string, error)
	// AvailabilityTTL bounds how long a provider probe is reused.
	// Defaults to 30s; probes never refresh mid-batch.
	AvailabilityTTL time.Duration
}

// Service fetches GitHub.com pull-request data and satisfies the Remote
// abstraction exposed by the desktop core. It holds no repository or
// credential state across calls: a token is obtained per operation and
// discarded.
type Service struct {
	baseURL string
	client  *http.Client
	look    func(string) (string, error)
	tokenFn func(context.Context) (string, error)
	ttl     time.Duration

	mu       sync.Mutex
	probedAt time.Time
	provider bool
}

// NewService builds a GitHub service.
func NewService(o Options) *Service {
	if o.BaseURL == "" {
		o.BaseURL = "https://api.github.com"
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 20 * time.Second}
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.AvailabilityTTL <= 0 {
		o.AvailabilityTTL = 30 * time.Second
	}
	return &Service{
		baseURL: o.BaseURL,
		client:  o.Client,
		look:    o.LookPath,
		tokenFn: o.TokenFn,
		ttl:     o.AvailabilityTTL,
	}
}

// Available reports whether a GitHub provider exists right now. The
// result is cached for AvailabilityTTL so the panel's periodic repo
// probe does not spawn gh every few seconds.
func (s *Service) Available(ctx context.Context) bool {
	s.mu.Lock()
	if !s.probedAt.IsZero() && time.Since(s.probedAt) < s.ttl {
		ok := s.provider
		s.mu.Unlock()
		return ok
	}
	s.mu.Unlock()

	_, err := s.token(ctx)
	ok := err == nil

	s.mu.Lock()
	s.provider = ok
	s.probedAt = time.Now()
	s.mu.Unlock()
	return ok
}
