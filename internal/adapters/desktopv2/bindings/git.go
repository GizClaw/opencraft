package bindings

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	crepo "github.com/GizClaw/opencraft/internal/capabilities/repo"
	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
)

// Git exposes read-only repository snapshots for the UI's Git panel.
// The binding stays stateless: every call resolves the repository from
// the active workspace and asks git itself (via gitx) so worktrees and
// user configuration behave exactly like the CLI the agent runs.
type Git struct {
	core *core.Core
}

// NewGitBinding wires the git binding.
func NewGitBinding(c *core.Core) *Git {
	return &Git{core: c}
}

// GitRepoDTO is the repository header for the Git panel.
type GitRepoDTO struct {
	InRepo    bool   `json:"in_repo"`
	Root      string `json:"root,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Workspace string `json:"workspace,omitempty"`
}

// Repo resolves the repository containing the active workspace. A
// non-repository workspace reports InRepo=false instead of failing, so
// the UI can hide the Git segment.
func (b *Git) Repo() GitRepoDTO {
	workDir := b.core.ActiveWorkDir()
	ctx := b.core.Shell.Context()
	dto := GitRepoDTO{Workspace: workDir}
	info := gitx.Info(ctx, workDir)
	if info.Root == "" {
		return dto
	}
	dto.InRepo = true
	dto.Root = info.Root
	dto.Branch = info.Branch
	ref := gitx.Ref(ctx, info.Root)
	if ref.Upstream != "" {
		dto.Upstream = ref.Upstream
		dto.Ahead = ref.Ahead
		dto.Behind = ref.Behind
	}
	return dto
}

// GitChangeDTO is one changed path in the status snapshot.
type GitChangeDTO struct {
	Path        string `json:"path"`
	OrigPath    string `json:"orig_path,omitempty"`
	Kind        string `json:"kind"`
	Staged      bool   `json:"staged"`
	Unstaged    bool   `json:"unstaged"`
	Untracked   bool   `json:"untracked"`
	Unmerged    bool   `json:"unmerged"`
	Directory   bool   `json:"directory"`
	IsBinary    bool   `json:"is_binary"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	InWorkspace bool   `json:"in_workspace"`
}

// GitStatusDTO is one bounded status snapshot.
type GitStatusDTO struct {
	Root      string         `json:"root,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Branch    string         `json:"branch,omitempty"`
	Truncated bool           `json:"truncated"`
	Entries   []GitChangeDTO `json:"entries"`
}

// Status returns the full repository change snapshot.
func (b *Git) Status() (GitStatusDTO, error) {
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return GitStatusDTO{}, errors.New("git: no workspace selected")
	}
	ctx := b.core.Shell.Context()
	info := gitx.Info(ctx, workDir)
	if info.Root == "" {
		return GitStatusDTO{
			Workspace: workDir,
		}, errors.New("git: workspace is not inside a git repository")
	}
	res := gitx.Status(ctx, info.Root, gitx.StatusOptions{})
	repoRoot, err := filepath.EvalSymlinks(info.Root)
	if err != nil {
		repoRoot = info.Root
	}
	workRoot, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		workRoot = workDir
	}
	entries := make([]GitChangeDTO, 0, len(res.Entries))
	for _, e := range res.Entries {
		entries = append(entries, GitChangeDTO{
			Path:        e.Path,
			OrigPath:    e.OrigPath,
			Kind:        string(e.Kind),
			Staged:      e.Staged,
			Unstaged:    e.Unstaged,
			Untracked:   e.Untracked,
			Unmerged:    e.Unmerged,
			Directory:   e.Directory,
			IsBinary:    e.IsBinary,
			Additions:   e.Additions,
			Deletions:   e.Deletions,
			InWorkspace: insideRoot(workRoot, repoRoot, e.Path),
		})
	}
	return GitStatusDTO{
		Root:      info.Root,
		Workspace: workDir,
		Branch:    info.Branch,
		Truncated: res.Truncated,
		Entries:   entries,
	}, nil
}

// GitLogEntryDTO is one row of the commit history.
type GitLogEntryDTO struct {
	OID      string `json:"oid"`
	ShortOID string `json:"short_oid"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Subject  string `json:"subject"`
}

// Log returns the newest commits of the repository.
func (b *Git) Log(limit int) ([]GitLogEntryDTO, error) {
	root, err := b.repoRoot()
	if err != nil {
		return nil, err
	}
	entries, truncated := gitx.Log(b.core.Shell.Context(), root, limit)
	if truncated {
		return nil, errors.New("git: history output exceeded the limit")
	}
	out := make([]GitLogEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, GitLogEntryDTO{
			OID:      e.OID,
			ShortOID: e.ShortOID,
			Author:   e.Author,
			Date:     e.Date,
			Subject:  e.Subject,
		})
	}
	return out, nil
}

// GitBranchDTO is one local branch row.
type GitBranchDTO struct {
	Name     string `json:"name"`
	Current  bool   `json:"current"`
	Upstream string `json:"upstream,omitempty"`
}

// Branches lists local branches.
func (b *Git) Branches() ([]GitBranchDTO, error) {
	root, err := b.repoRoot()
	if err != nil {
		return nil, err
	}
	branches := gitx.Branches(b.core.Shell.Context(), root)
	out := make([]GitBranchDTO, 0, len(branches))
	for _, br := range branches {
		out = append(out, GitBranchDTO{
			Name:     br.Name,
			Current:  br.Current,
			Upstream: br.Upstream,
		})
	}
	return out, nil
}

// GitDiffDTO is one bounded unified diff.
type GitDiffDTO struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// Diff returns the working-tree or staged unified diff for one
// repository-relative path. Untracked paths have no diff.
func (b *Git) Diff(path string, cached bool) (GitDiffDTO, error) {
	if strings.TrimSpace(path) == "" {
		return GitDiffDTO{}, errors.New("git: diff path is required")
	}
	root, err := b.repoRoot()
	if err != nil {
		return GitDiffDTO{}, err
	}
	content, truncated := gitx.Diff(
		b.core.Shell.Context(), root, path, cached, 0)
	return GitDiffDTO{Content: content, Truncated: truncated}, nil
}

// GitCommitFileDTO is one path changed by a commit.
type GitCommitFileDTO struct {
	Path      string `json:"path"`
	OrigPath  string `json:"orig_path,omitempty"`
	Kind      string `json:"kind"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	IsBinary  bool   `json:"is_binary"`
}

// GitCommitFilesDTO is one bounded commit change snapshot.
type GitCommitFilesDTO struct {
	Files     []GitCommitFileDTO `json:"files"`
	Truncated bool               `json:"truncated"`
}

// CommitFiles lists the files one commit changed (merge commits
// compare against their first parent, like CommitDiff).
func (b *Git) CommitFiles(
	oid string,
) (GitCommitFilesDTO, error) {
	if strings.TrimSpace(oid) == "" {
		return GitCommitFilesDTO{}, errors.New(
			"git: commit id is required")
	}
	root, err := b.repoRoot()
	if err != nil {
		return GitCommitFilesDTO{}, err
	}
	res := gitx.CommitFiles(b.core.Shell.Context(), root, strings.TrimSpace(oid))
	out := make([]GitCommitFileDTO, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, GitCommitFileDTO{
			Path:      f.Path,
			OrigPath:  f.OrigPath,
			Kind:      string(f.Kind),
			Additions: f.Additions,
			Deletions: f.Deletions,
			IsBinary:  f.IsBinary,
		})
	}
	return GitCommitFilesDTO{Files: out, Truncated: res.Truncated}, nil
}

// CommitDiff returns a bounded unified diff for one path inside one
// commit, using the same first-parent semantics as CommitFiles.
func (b *Git) CommitDiff(
	oid, path string,
) (GitDiffDTO, error) {
	if strings.TrimSpace(oid) == "" || strings.TrimSpace(path) == "" {
		return GitDiffDTO{}, errors.New(
			"git: commit id and diff path are required")
	}
	root, err := b.repoRoot()
	if err != nil {
		return GitDiffDTO{}, err
	}
	content, truncated := gitx.CommitDiff(
		b.core.Shell.Context(), root,
		strings.TrimSpace(oid), strings.TrimSpace(path), 0)
	return GitDiffDTO{Content: content, Truncated: truncated}, nil
}

// repoRoot resolves the repository root for the active workspace.
func (b *Git) repoRoot() (string, error) {
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return "", errors.New("git: no workspace selected")
	}
	info := gitx.Info(b.core.Shell.Context(), workDir)
	if info.Root == "" {
		return "", errors.New("git: workspace is not inside a git repository")
	}
	return info.Root, nil
}

// runWrite gates one repository mutation: the UI is disabled while a
// turn runs, and the backend refuses writes when any active run still
// exists in this workspace (including parallel turns or automations).
func (b *Git) runWrite(op crepo.Op) (string, error) {
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return "", errors.New("git: no workspace selected")
	}
	if h := b.core.Runtime.Current(); h != nil && len(h.ActiveRuns()) > 0 {
		return "", errors.New(
			"git: the agent is still running in this workspace; " +
				"wait for the turn to finish before changing the repository")
	}
	root, err := b.repoRoot()
	if err != nil {
		return "", err
	}
	layout, err := b.core.ResolveLayout(workDir)
	if err != nil {
		return "", err
	}
	res, err := b.core.Git.Run(b.core.Shell.Context(), crepo.Request{
		Root:     root,
		AuditDir: layout.AuditDir,
		Op:       op,
	})
	if err != nil {
		return "", err
	}
	b.core.Shell.Emit("git_changed", map[string]string{"repo_root": root})
	return res.Output, nil
}

// Stage stages the given repo-relative paths (or adds them to the
// index including deletions).
func (b *Git) Stage(paths []string) (string, error) {
	return b.runWrite(crepo.Op{Kind: crepo.KindStage, Paths: paths})
}

// Unstage unstages the given repo-relative paths.
func (b *Git) Unstage(paths []string) (string, error) {
	return b.runWrite(crepo.Op{Kind: crepo.KindUnstage, Paths: paths})
}

// Commit creates a commit with the given message from the index.
func (b *Git) Commit(message string) (string, error) {
	return b.runWrite(crepo.Op{
		Kind:    crepo.KindCommit,
		Message: strings.TrimSpace(message),
	})
}

// Checkout switches to an existing local branch.
func (b *Git) Checkout(branch string) (string, error) {
	return b.runWrite(crepo.Op{
		Kind:   crepo.KindCheckout,
		Branch: strings.TrimSpace(branch),
	})
}

// NewBranch creates and checks out a new branch.
func (b *Git) NewBranch(name string) (string, error) {
	return b.runWrite(crepo.Op{
		Kind:   crepo.KindNewBranch,
		Branch: strings.TrimSpace(name),
	})
}

// Discard reverts working-tree changes for the given paths; staged
// additionally reverts the index, destroying staged content.
func (b *Git) Discard(paths []string, staged bool) (string, error) {
	kind := crepo.KindDiscardWorktree
	if staged {
		kind = crepo.KindDiscardBoth
	}
	return b.runWrite(crepo.Op{Kind: kind, Paths: paths})
}

// Clean removes untracked files/directories for the given paths.
func (b *Git) Clean(paths []string) (string, error) {
	return b.runWrite(crepo.Op{Kind: crepo.KindClean, Paths: paths})
}

// Pull fetches and integrates the current branch's upstream.
func (b *Git) Pull() (string, error) {
	return b.runWrite(crepo.Op{Kind: crepo.KindPull})
}

// Push uploads the current branch; force uses --force-with-lease and
// must only be called after the UI's strong confirmation.
func (b *Git) Push(force bool) (string, error) {
	if force {
		return b.runWrite(crepo.Op{Kind: crepo.KindForcePush})
	}
	return b.runWrite(crepo.Op{Kind: crepo.KindPush})
}

// insideRoot reports whether the repo-relative path lives under root.
func insideRoot(root, repoRoot, rel string) bool {
	if root == "" || repoRoot == "" {
		return false
	}
	full := filepath.Join(repoRoot, filepath.FromSlash(rel))
	relTo, err := filepath.Rel(root, full)
	if err != nil {
		return false
	}
	return relTo != ".." &&
		!strings.HasPrefix(relTo, ".."+string(filepath.Separator))
}
