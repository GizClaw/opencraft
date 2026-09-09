package core

import (
	"context"

	crepo "github.com/GizClaw/opencraft/internal/capabilities/repo"
	gitxgh "github.com/GizClaw/opencraft/internal/foundation/utils/gitx/gh"
)

// Remote abstracts one remote code host behind the Git panel's PR
// view. Repositories are addressed by their owner/repo pair. The
// abstraction lives here, at the consumer, so core names no concrete
// provider: the default implementation is the gh-backed GitHub Service
// in foundation/utils/gitx/gh, and future hosts implement the same
// contract.
type Remote interface {
	// Available reports whether a usable provider session exists right
	// now (gh installed and logged in for the host).
	Available(ctx context.Context) bool
	// List returns the newest open pull requests of one repository.
	List(ctx context.Context, owner, repo string) ([]gitxgh.Pull, error)
	// Detail loads one full pull-request page.
	Detail(
		ctx context.Context,
		owner, repo string,
		number int,
	) (*gitxgh.Detail, error)
}

// GitService is the desktop gateway for repository operations: local
// mutations run through capabilities/repo, while Remote() exposes the
// code-host provider abstraction for the read-only PR view. The
// service itself carries no repository state.
type GitService struct {
	remote Remote
}

// NewGitService builds the git gateway with the default remote
// provider (GitHub.com via the system gh CLI).
func NewGitService() *GitService {
	return &GitService{remote: gitxgh.NewService(gitxgh.Options{})}
}

// NewGitServiceWithRemote wires an alternate remote provider. Tests
// inject an httptest-backed GitHub service here.
func NewGitServiceWithRemote(remote Remote) *GitService {
	if remote == nil {
		remote = gitxgh.NewService(gitxgh.Options{})
	}
	return &GitService{remote: remote}
}

// Remote returns the code-host abstraction backing the Git panel's PR
// view. Concrete providers (the gh-backed GitHub Service, future hosts)
// implement Remote; bindings never construct or name a provider.
func (s *GitService) Remote() Remote {
	if s == nil || s.remote == nil {
		return gitxgh.NewService(gitxgh.Options{})
	}
	return s.remote
}

// Run executes one bounded repository mutation.
func (s *GitService) Run(
	ctx context.Context,
	req crepo.Request,
) (crepo.Result, error) {
	return crepo.Run(ctx, req)
}
