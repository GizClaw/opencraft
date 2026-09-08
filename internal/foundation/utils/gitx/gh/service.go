package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// token returns one batch token from the injected TokenFn or from gh.
// The token is scoped to the current List/Detail call; it is never
// stored, logged or sent to the UI.
func (s *Service) token(ctx context.Context) (string, error) {
	if s.tokenFn != nil {
		return s.tokenFn(ctx)
	}
	return ghAuthToken(ctx, s.look)
}

// ghAuthToken asks gh for the github.com token, following lazygit's
// strategy of re-resolving the current account for every batch. GH_TOKEN
// / GITHUB_TOKEN are stripped from the child environment so a stale env
// token can never masquerade as an authenticated provider.
func ghAuthToken(
	ctx context.Context,
	lookPath func(string) (string, error),
) (string, error) {
	bin, err := ghExecutable(lookPath)
	if err != nil {
		return "", ErrNoProvider
	}
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin,
		"auth", "token", "--hostname", "github.com")
	cmd.Env = stripTokenEnv()
	var out limitedBuffer
	out.max = 16 << 10
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", ErrNoProvider
	}
	token := strings.TrimSpace(out.String())
	if token == "" || out.overflow || len(token) > 4096 {
		return "", ErrNoProvider
	}
	return token, nil
}

// ghExecutable resolves the gh CLI binary. An explicit GH_PATH wins
// (lazygit's convention), then PATH, then well-known per-platform
// install locations. The candidate fallback keeps the PR view working
// when the app is launched outside a login shell (for example a macOS
// GUI app whose PATH is the launchd default) and the CLI lives in a
// Homebrew or other user-local directory.
func ghExecutable(lookPath func(string) (string, error)) (string, error) {
	return ghExecutableFrom(
		lookPath, os.Getenv("GH_PATH"), ghCandidatePaths())
}

// ghExecutableFrom is ghExecutable with the override and candidate list
// injected so resolution can be tested without touching the host PATH.
func ghExecutableFrom(
	lookPath func(string) (string, error),
	override string,
	candidates []string,
) (string, error) {
	if override = strings.TrimSpace(override); override != "" &&
		usableGHBinary(override) {
		return override, nil
	}
	if bin, err := lookPath("gh"); err == nil {
		return bin, nil
	}
	for _, candidate := range candidates {
		if usableGHBinary(candidate) {
			return candidate, nil
		}
	}
	return "", ErrNoProvider
}

// ghCandidatePaths lists the standard gh install directories for each
// platform, probed after PATH misses.
func ghCandidatePaths() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			// Official MSI / winget install.
			filepath.Join(os.Getenv("ProgramFiles"), "GitHub CLI", "gh.exe"),
			// Per-user installs (winget --scope user and friends).
			filepath.Join(
				os.Getenv("LOCALAPPDATA"), "Programs", "GitHub CLI", "gh.exe"),
		}
	default:
		candidates := []string{
			// Apple Silicon Homebrew.
			"/opt/homebrew/bin/gh",
			// Intel Homebrew and classic /usr/local installs.
			"/usr/local/bin/gh",
			// Linux distro packages.
			"/usr/bin/gh",
			// Linux snap installs.
			"/snap/bin/gh",
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				// Linuxbrew and user-local installs.
				filepath.Join(home, ".linuxbrew", "bin", "gh"),
				filepath.Join(home, ".local", "bin", "gh"),
			)
		}
		return candidates
	}
}

// usableGHBinary reports whether path names an existing gh executable
// the process may run. Windows has no executable permission bit; every
// other platform requires at least one execute bit.
func usableGHBinary(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// stripTokenEnv removes token environment variables from the gh child
// so provider status means an actual `gh auth login` session.
func stripTokenEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		switch key {
		case "GH_TOKEN", "GITHUB_TOKEN":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// limitedBuffer is a write-capped buffer used for subprocess output.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.max <= 0 {
		w.max = 64 << 10
	}
	room := w.max - w.buf.Len()
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

func (w *limitedBuffer) String() string { return w.buf.String() }

// List returns the newest open pull requests of one repository.
func (s *Service) List(
	ctx context.Context,
	owner, repo string,
) ([]Pull, error) {
	if !validName(owner) || !validName(repo) {
		return nil, errors.New("github: invalid owner/repository name")
	}
	token, err := s.token(ctx)
	if err != nil {
		return nil, providerErr(err)
	}
	batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := newAPIClient(s.baseURL, s.client, token)
	return c.listPRs(batchCtx, owner, repo)
}

// Detail loads the full PR page: header, commits, CI checks,
// conversation and inline review threads. Sub-resources are fetched in
// parallel under one token and bounded page/byte limits.
func (s *Service) Detail(
	ctx context.Context,
	owner, repo string,
	number int,
) (*Detail, error) {
	if !validName(owner) || !validName(repo) || number <= 0 ||
		number > 1_000_000_000 {
		return nil, errors.New("github: invalid pull request identifier")
	}
	token, err := s.token(ctx)
	if err != nil {
		return nil, providerErr(err)
	}
	batchCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	c := newAPIClient(s.baseURL, s.client, token)

	var (
		pr      ghPull
		commits []ghCommit
		issue   []ghIssueComment
		reviews []ghReview
		inlines []ghReviewComment
	)
	err = parallel(batchCtx,
		func() error {
			p, e := c.pull(batchCtx, owner, repo, number)
			if e == nil {
				pr = p
			}
			return e
		},
		func() error {
			rows, e := c.commits(batchCtx, owner, repo, number)
			if e == nil {
				commits = rows
			}
			return e
		},
		func() error {
			rows, e := c.issueComments(batchCtx, owner, repo, number)
			if e == nil {
				issue = rows
			}
			return e
		},
		func() error {
			rows, e := c.reviews(batchCtx, owner, repo, number)
			if e == nil {
				reviews = rows
			}
			return e
		},
		func() error {
			rows, e := c.reviewComments(batchCtx, owner, repo, number)
			if e == nil {
				inlines = rows
			}
			return e
		},
	)
	if err != nil {
		return nil, err
	}

	// CI checks need the PR head SHA, so they run after the header
	// fetch succeeded.
	var checks []Check
	if pr.Head.SHA != "" {
		checks, err = c.checks(batchCtx, owner, repo, pr.Head.SHA)
		if err != nil {
			return nil, err
		}
	}

	d := &Detail{
		Pull:           pullFromGH(pr),
		Body:           pr.Body,
		Mergeable:      pr.Mergeable != nil && *pr.Mergeable,
		MergeableState: pr.MergeableState,
		ChangedFiles:   pr.ChangedFiles,
		Additions:      pr.Additions,
		Deletions:      pr.Deletions,
		CommitsCount:   pr.Commits,
		Commits:        commitsFromGH(commits),
		Checks:         checks,
		Conversation:   timelineFromGH(issue, reviews),
		Threads:        threadsFromGH(inlines),
		Truncated: c.commitsTruncated || c.issueTruncated ||
			c.inlineTruncated ||
			(pr.Commits > 0 && len(commits) < pr.Commits),
	}
	return d, nil
}

func validName(s string) bool {
	return s != "" && len(s) <= 100 && nameRe.MatchString(s)
}

// providerErr preserves ErrNoProvider semantics while keeping a
// non-provider token failure diagnosable.
func providerErr(err error) error {
	if errors.Is(err, ErrNoProvider) {
		return ErrNoProvider
	}
	return fmt.Errorf("%w: %v", ErrNoProvider, err)
}

// parallel runs jobs concurrently and returns every failure joined.
func parallel(ctx context.Context, jobs ...func() error) error {
	var wg sync.WaitGroup
	errs := make([]error, len(jobs))
	for i, job := range jobs {
		if job == nil {
			continue
		}
		wg.Add(1)
		go func(i int, job func() error) {
			defer wg.Done()
			errs[i] = job()
		}(i, job)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	var out []error
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return errors.Join(out...)
}

func pullFromGH(p ghPull) Pull {
	state := p.State
	if p.State == "closed" && p.MergedAt != nil && *p.MergedAt != "" {
		state = "merged"
	}
	return Pull{
		Number:    p.Number,
		Title:     p.Title,
		State:     state,
		Draft:     p.Draft,
		Author:    Author{Login: p.User.Login, AvatarURL: p.User.AvatarURL},
		Base:      p.Base.Ref,
		Head:      p.Head.Ref,
		HeadSHA:   p.Head.SHA,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		HTMLURL:   p.HTMLURL,
	}
}

func commitsFromGH(commits []ghCommit) []Commit {
	out := make([]Commit, 0, len(commits))
	for _, c := range commits {
		short := c.SHA
		if len(short) > 7 {
			short = short[:7]
		}
		out = append(out, Commit{
			SHA:      c.SHA,
			ShortSHA: short,
			Message:  c.Commit.Message,
			Author:   c.Commit.Author.Name,
			Date:     c.Commit.Author.Date,
		})
	}
	return out
}

// timelineFromGH merges issue comments and submitted review summaries
// into one chronological conversation, mirroring the GitHub timeline.
func timelineFromGH(
	issue []ghIssueComment,
	reviews []ghReview,
) []TimelineItem {
	items := make([]TimelineItem, 0, len(issue)+len(reviews))
	for _, c := range issue {
		items = append(items, TimelineItem{
			ID:        c.ID,
			Kind:      "comment",
			Author:    Author{Login: c.User.Login, AvatarURL: c.User.AvatarURL},
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			HTMLURL:   c.HTMLURL,
		})
	}
	for _, r := range reviews {
		// PENDING entries are unsent review drafts; GitHub never shows
		// them on the conversation timeline.
		if r.State == "PENDING" || r.SubmittedAt == "" {
			continue
		}
		items = append(items, TimelineItem{
			ID:        r.ID,
			Kind:      "review",
			Author:    Author{Login: r.User.Login, AvatarURL: r.User.AvatarURL},
			Body:      r.Body,
			Action:    r.State,
			CreatedAt: r.SubmittedAt,
			HTMLURL:   r.HTMLURL,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt < items[j].CreatedAt
	})
	return items
}

// threadsFromGH groups review comments into anchored discussions. Every
// comment without in_reply_to_id starts a thread; replies are attached
// to their root and sorted by creation time. GitHub reports each reply
// with the id of the comment it directly answers, so reply-of-reply
// chains are walked up to their root instead of being dropped as
// orphan threads.
func threadsFromGH(comments []ghReviewComment) []Thread {
	threadOf := make(map[int64]int, len(comments))
	// parentOf links every reply to the comment it directly answers.
	parentOf := make(map[int64]int64, len(comments))
	var threads []Thread
	for i := range comments {
		c := &comments[i]
		if c.InReplyToID != nil {
			parentOf[c.ID] = *c.InReplyToID
			continue
		}
		t := Thread{
			Path:              c.Path,
			Side:              c.Side,
			Line:              intOrZero(c.Line),
			OriginalLine:      intOrZero(c.OriginalLine),
			StartLine:         intOrZero(c.StartLine),
			OriginalStartLine: intOrZero(c.OriginalStartLine),
			DiffHunk:          c.DiffHunk,
			Comments: []Comment{{
				ID:        c.ID,
				Author:    Author{Login: c.User.Login, AvatarURL: c.User.AvatarURL},
				Body:      c.Body,
				CreatedAt: c.CreatedAt,
				HTMLURL:   c.HTMLURL,
			}},
		}
		threads = append(threads, t)
		threadOf[c.ID] = len(threads) - 1
	}
	for i := range comments {
		c := &comments[i]
		if c.InReplyToID == nil {
			continue
		}
		comment := Comment{
			ID:        c.ID,
			Author:    Author{Login: c.User.Login, AvatarURL: c.User.AvatarURL},
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			HTMLURL:   c.HTMLURL,
		}
		// Resolve the direct parent and walk nested replies up to the
		// thread's root. The hop bound guards against a corrupt
		// in_reply_to_id cycle.
		parent := *c.InReplyToID
		idx, ok := threadOf[parent]
		for hops := 0; !ok && hops < len(comments); hops++ {
			next, hasParent := parentOf[parent]
			if !hasParent {
				break
			}
			parent = next
			idx, ok = threadOf[parent]
		}
		if !ok {
			// Orphan reply (parent beyond page cap or corrupt chain):
			// keep the reply as its own thread so the content is never
			// dropped silently.
			threads = append(threads, Thread{
				Path:              c.Path,
				Side:              c.Side,
				Line:              intOrZero(c.Line),
				OriginalLine:      intOrZero(c.OriginalLine),
				StartLine:         intOrZero(c.StartLine),
				OriginalStartLine: intOrZero(c.OriginalStartLine),
				DiffHunk:          c.DiffHunk,
				Comments:          []Comment{comment},
			})
			continue
		}
		threads[idx].Comments = append(threads[idx].Comments, comment)
	}
	sort.SliceStable(threads, func(i, j int) bool {
		a, b := threads[i], threads[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		al, bl := anchorLine(a), anchorLine(b)
		if al != bl {
			return al < bl
		}
		return a.Side < b.Side
	})
	for i := range threads {
		sort.SliceStable(threads[i].Comments, func(x, y int) bool {
			return threads[i].Comments[x].CreatedAt <
				threads[i].Comments[y].CreatedAt
		})
	}
	return threads
}

func anchorLine(t Thread) int {
	if t.Side == "LEFT" && t.OriginalLine > 0 {
		return t.OriginalLine
	}
	return t.Line
}

func intOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func errf(op, detail string) error {
	if detail == "" {
		return fmt.Errorf("github: %s", op)
	}
	return fmt.Errorf("github: %s: %s", op, capText(detail))
}

func capText(s string) string {
	if len(s) <= 512 {
		return s
	}
	return s[:512] + "…"
}
